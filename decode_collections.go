package jsn

import (
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strconv"
	"unsafe"
)

func buildSequenceDecoder(typ reflect.Type, visiting map[reflect.Type]*decodeFunc) decodeFunc {
	element := typ.Elem()
	decode := buildDecoder(element, visiting)
	size := element.Size()
	isSlice := typ.Kind() == reflect.Slice
	byteSlice := isSlice && element.Kind() == reflect.Uint8 && !reflect.PointerTo(element).Implements(unmarshalerType) && !reflect.PointerTo(element).Implements(textUnmarshalerType)
	initialCapacity := 8

	if size > 32 || element.Kind() == reflect.Pointer {
		initialCapacity = 1
	}

	return func(state *decodeState, dst unsafe.Pointer) error {
		first := state.space()
		if first == 'n' {
			err := state.literal("null")
			if err == nil && isSlice {
				*(*sliceHeader)(dst) = sliceHeader{}
			}

			return err
		}

		if first == '"' && byteSlice {
			start := state.pos

			raw, simple, err := state.stringToken()
			if err != nil {
				return err
			}

			if !simple {
				raw = []byte(decodeString(raw, simple))
			}

			buffer := make([]byte, base64.StdEncoding.DecodedLen(len(raw)))

			length, err := base64.StdEncoding.Decode(buffer, raw)
			if err != nil {
				state.pos = start

				return buildStandardDecoder(typ)(state, dst)
			}

			*(*[]byte)(dst) = buffer[:length]

			return nil
		}

		if first != '[' {
			return state.mismatch(typ)
		}

		err := state.enter()
		if err != nil {
			return err
		}

		value := reflect.NewAt(typ, dst).Elem()

		if state.space() == ']' {
			if isSlice {
				value.Set(reflect.MakeSlice(typ, 0, 0))
			} else {
				value.SetZero()
			}

			state.pos++
			state.depth--

			return nil
		}

		length := 0

		for {
			var address unsafe.Pointer

			if isSlice {
				header := (*sliceHeader)(dst)
				if length == header.Cap {
					growth := 1

					if header.Cap == 0 {
						// Batch small elements, but avoid overallocating pointer
						// slices in single-child recursive structures.
						growth = initialCapacity
					}

					value.Grow(growth)
				}

				if length >= header.Len {
					header.Len = length + 1
				}

				address = unsafe.Add(header.Data, uintptr(length)*size)
			} else if length < typ.Len() {
				address = unsafe.Add(dst, uintptr(length)*size)
			}

			if address == nil {
				err = state.skip()
			} else {
				err = decode(state, address)
			}

			if err != nil {
				return err
			}

			length++

			switch state.space() {
			case ']':
				if isSlice {
					(*sliceHeader)(dst).Len = length
				} else {
					for index := length; index < typ.Len(); index++ {
						value.Index(index).SetZero()
					}
				}

				state.pos++
				state.depth--

				return nil
			case ',':
				state.pos++
				state.space()
			default:
				return errDecodeSyntax
			}
		}
	}
}

func buildMapDecoder(typ reflect.Type, visiting map[reflect.Type]*decodeFunc) decodeFunc {
	if typ.Key() != reflect.TypeFor[string]() {
		return buildStandardDecoder(typ)
	}

	decode := buildDecoder(typ.Elem(), visiting)

	switch typ.Elem() {
	case reflect.TypeFor[string]():
		return buildStringMapDecoder[string](typ, decode)
	case reflect.TypeFor[int]():
		return buildStringMapDecoder[int](typ, decode)
	case reflect.TypeFor[int64]():
		return buildStringMapDecoder[int64](typ, decode)
	case reflect.TypeFor[uint64]():
		return buildStringMapDecoder[uint64](typ, decode)
	case reflect.TypeFor[float64]():
		return buildStringMapDecoder[float64](typ, decode)
	case reflect.TypeFor[bool]():
		return buildStringMapDecoder[bool](typ, decode)
	case reflect.TypeFor[any]():
		return buildStringMapDecoder[any](typ, decode)
	default:
		return buildReflectMapDecoder(typ, decode)
	}
}

func buildStringMapDecoder[Value any](typ reflect.Type, decode decodeFunc) decodeFunc {
	return func(state *decodeState, dst unsafe.Pointer) error {
		if state.space() == 'n' {
			err := state.literal("null")
			if err == nil {
				*(*map[string]Value)(dst) = nil
			}

			return err
		}

		if state.space() != '{' {
			return state.mismatch(typ)
		}

		err := state.enter()
		if err != nil {
			return err
		}

		pointer := (*map[string]Value)(dst)
		if *pointer == nil {
			*pointer = make(map[string]Value)
		}

		if state.space() == '}' {
			state.pos++
			state.depth--

			return nil
		}

		var value Value

		for {
			raw, simple, err := state.stringToken()
			if err != nil {
				return err
			}

			key := decodeString(raw, simple)

			if state.space() != ':' {
				return errDecodeSyntax
			}

			state.pos++
			state.space()

			var zero Value

			value = zero

			err = decode(state, unsafe.Pointer(&value))
			if err != nil {
				return err
			}

			(*pointer)[key] = value

			switch state.space() {
			case '}':
				state.pos++
				state.depth--

				return nil
			case ',':
				state.pos++
				state.space()
			default:
				return errDecodeSyntax
			}
		}
	}
}

func buildReflectMapDecoder(typ reflect.Type, decode decodeFunc) decodeFunc {
	return func(state *decodeState, dst unsafe.Pointer) error {
		if state.space() == 'n' {
			err := state.literal("null")
			if err == nil {
				reflect.NewAt(typ, dst).Elem().SetZero()
			}

			return err
		}

		if state.space() != '{' {
			return state.mismatch(typ)
		}

		err := state.enter()
		if err != nil {
			return err
		}

		value := reflect.NewAt(typ, dst).Elem()
		if value.IsNil() {
			value.Set(reflect.MakeMap(typ))
		}

		if state.space() == '}' {
			state.pos++
			state.depth--

			return nil
		}

		element := reflect.New(typ.Elem())
		address := element.UnsafePointer()
		key := reflect.New(typ.Key()).Elem()

		for {
			raw, simple, err := state.stringToken()
			if err != nil {
				return err
			}

			key.SetString(decodeString(raw, simple))

			if state.space() != ':' {
				return errDecodeSyntax
			}

			state.pos++
			state.space()
			element.Elem().SetZero()

			err = decode(state, address)
			if err != nil {
				return err
			}

			value.SetMapIndex(key, element.Elem())

			switch state.space() {
			case '}':
				state.pos++
				state.depth--

				return nil
			case ',':
				state.pos++
				state.space()
			default:
				return errDecodeSyntax
			}
		}
	}
}

func decodeInterface(state *decodeState, dst unsafe.Pointer) error {
	value := (*any)(dst)
	if *value != nil {
		current := reflect.ValueOf(*value)
		if current.Kind() == reflect.Pointer && !current.IsNil() {
			return buildStandardDecoder(reflect.TypeFor[any]())(state, dst)
		}
	}

	decoded, err := state.anyValue()
	if err == nil {
		*value = decoded
	}

	return err
}

func (state *decodeState) anyValue() (any, error) {
	switch state.space() {
	case 'n':
		return nil, state.literal("null")
	case 't':
		return true, state.literal("true")
	case 'f':
		return false, state.literal("false")
	case '"':
		raw, simple, err := state.stringToken()
		if err != nil {
			return nil, err
		}

		return decodeString(raw, simple), nil
	case '[':
		err := state.enter()
		if err != nil {
			return nil, err
		}

		values := make([]any, 0)

		if state.space() == ']' {
			state.pos++
			state.depth--

			return values, nil
		}

		for {
			value, err := state.anyValue()
			if err != nil {
				return nil, err
			}

			values = append(values, value)

			switch state.space() {
			case ']':
				state.pos++
				state.depth--

				return values, nil
			case ',':
				state.pos++
			default:
				return nil, errDecodeSyntax
			}
		}
	case '{':
		err := state.enter()
		if err != nil {
			return nil, err
		}

		values := make(map[string]any)

		if state.space() == '}' {
			state.pos++
			state.depth--

			return values, nil
		}

		for {
			raw, simple, err := state.stringToken()
			if err != nil {
				return nil, err
			}

			key := decodeString(raw, simple)

			if state.space() != ':' {
				return nil, errDecodeSyntax
			}

			state.pos++

			value, err := state.anyValue()
			if err != nil {
				return nil, err
			}

			values[key] = value

			switch state.space() {
			case '}':
				state.pos++
				state.depth--

				return values, nil
			case ',':
				state.pos++
				state.space()
			default:
				return nil, errDecodeSyntax
			}
		}
	default:
		raw, err := state.numberToken()
		if err != nil {
			return nil, err
		}

		if state.useNumber {
			return json.Number(raw), nil
		}

		value, err := strconv.ParseFloat(string(raw), 64)
		if err != nil {
			offset := state.pos

			if legacyDecodeErrors {
				offset++
			}

			state.save(&json.UnmarshalTypeError{Value: "number " + string(raw), Type: reflect.TypeFor[float64](), Offset: int64(offset)})

			if legacyDecodeErrors {
				return nil, nil
			}

			return float64(0), nil
		}

		return value, nil
	}
}
