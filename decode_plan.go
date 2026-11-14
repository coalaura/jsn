package jsn

import (
	"bytes"
	"encoding"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"unsafe"
)

type decodeField struct {
	name    string
	parents []uintptr
	types   []reflect.Type
	offset  uintptr
	decode  decodeFunc
}

type decodeObject struct {
	fields []decodeField
	exact  map[string]int
	typ    reflect.Type
}

var (
	unmarshalerType     = reflect.TypeFor[json.Unmarshaler]()
	textUnmarshalerType = reflect.TypeFor[encoding.TextUnmarshaler]()
)

func (object *decodeObject) decode(state *decodeState, dst unsafe.Pointer) error {
	if state.space() == 'n' {
		return state.literal("null")
	}

	if state.space() != '{' {
		return state.mismatch(object.typ)
	}

	err := state.enter()
	if err != nil {
		return err
	}

	if state.space() == '}' {
		state.pos++
		state.depth--

		return nil
	}

	expected := 0

	for {
		raw, simple, err := state.stringToken()
		if err != nil {
			return err
		}

		index := -1

		if simple && expected < len(object.fields) && string(raw) == object.fields[expected].name {
			index = expected
		} else if simple {
			if found, ok := object.exact[string(raw)]; ok {
				index = found
			} else {
				index = object.folded(string(raw))
			}
		} else {
			name := decodeString(raw, simple)
			if found, ok := object.exact[name]; ok {
				index = found
			} else {
				index = object.folded(name)
			}
		}

		if state.space() != ':' {
			return errDecodeSyntax
		}

		state.pos++
		state.space()

		if index < 0 {
			if state.disallowUnknown {
				state.save(fmt.Errorf("json: unknown field %q", decodeString(raw, simple)))
			}

			err = state.skip()
		} else {
			field := &object.fields[index]
			parent := dst

			for position, offset := range field.parents {
				pointer := (*unsafe.Pointer)(unsafe.Add(parent, offset))
				if *pointer == nil {
					*pointer = reflect.New(field.types[position]).UnsafePointer()
				}

				parent = *pointer
			}

			clean := state.saved == nil
			err = field.decode(state, unsafe.Add(parent, field.offset))

			if clean && state.saved != nil {
				mismatch, ok := errors.AsType[*json.UnmarshalTypeError](state.saved)
				if ok {
					name := field.name

					if !legacyDecodeErrors {
						name = decodeString(raw, simple)
					}

					if mismatch.Struct == "" {
						mismatch.Struct = object.typ.Name()
					}

					if mismatch.Field == "" {
						mismatch.Field = name
					} else {
						mismatch.Field = name + "." + mismatch.Field
					}
				}
			}

			expected = index + 1
		}

		if err != nil {
			return err
		}

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

func (object *decodeObject) folded(name string) int {
	for index := range object.fields {
		if strings.EqualFold(name, object.fields[index].name) {
			return index
		}
	}

	return -1
}

func (state *decodeState) mismatch(typ reflect.Type) error {
	start := state.pos

	err := state.skip()
	if err != nil {
		return err
	}

	value := "number"

	kind := typ.Kind()
	if kind >= reflect.Int && kind <= reflect.Float64 {
		value += " " + string(state.data[start:state.pos])
	}

	offset := state.pos

	switch state.data[start] {
	case '{':
		value = "object"
		offset = start + 1
	case '[':
		value = "array"
		offset = start + 1
	case '"':
		value = "string"
	case 't', 'f':
		value = "bool"
	case 'n':
		value = "null"
	}

	state.save(&json.UnmarshalTypeError{Value: value, Type: typ, Offset: int64(offset)})

	return nil
}

func buildDecoder(typ reflect.Type, visiting map[reflect.Type]*decodeFunc) decodeFunc {
	if previous, ok := visiting[typ]; ok {
		return func(state *decodeState, dst unsafe.Pointer) error {
			return (*previous)(state, dst)
		}
	}

	var decode decodeFunc

	visiting[typ] = &decode
	decode = buildDecodeKind(typ, visiting)

	return decode
}

func buildDecodeKind(typ reflect.Type, visiting map[reflect.Type]*decodeFunc) decodeFunc {
	if typ.Kind() == reflect.Pointer {
		element := typ.Elem()
		decode := buildDecoder(element, visiting)

		return func(state *decodeState, dst unsafe.Pointer) error {
			pointer := (*unsafe.Pointer)(dst)

			if state.space() == 'n' {
				err := state.literal("null")
				if err == nil {
					*pointer = nil
				}

				return err
			}

			if *pointer == nil {
				*pointer = reflect.New(element).UnsafePointer()
			}

			return decode(state, *pointer)
		}
	}

	pointerType := reflect.PointerTo(typ)
	if pointerType.Implements(unmarshalerType) {
		return func(state *decodeState, dst unsafe.Pointer) error {
			start := state.pos

			err := state.skip()
			if err != nil {
				return err
			}

			value := reflect.NewAt(typ, dst).Interface().(json.Unmarshaler)

			err = value.UnmarshalJSON(state.data[start:state.pos])

			if !legacyDecodeErrors {
				state.save(err)

				return nil
			}

			return err
		}
	}

	if pointerType.Implements(textUnmarshalerType) {
		return func(state *decodeState, dst unsafe.Pointer) error {
			if state.space() == 'n' {
				return state.literal("null")
			}

			if state.space() != '"' {
				return state.mismatch(typ)
			}

			raw, simple, err := state.stringToken()
			if err != nil {
				return err
			}

			value := reflect.NewAt(typ, dst).Interface().(encoding.TextUnmarshaler)

			err = value.UnmarshalText([]byte(decodeString(raw, simple)))

			if !legacyDecodeErrors {
				state.save(err)

				return nil
			}

			return err
		}
	}

	if typ == numberType {
		return decodeNumber
	}

	switch typ.Kind() {
	case reflect.Bool:
		return buildBoolDecoder(typ)
	case reflect.String:
		return buildStringDecoder(typ)
	case reflect.Int:
		return buildSignedDecoder[int](typ)
	case reflect.Int8:
		return buildSignedDecoder[int8](typ)
	case reflect.Int16:
		return buildSignedDecoder[int16](typ)
	case reflect.Int32:
		return buildSignedDecoder[int32](typ)
	case reflect.Int64:
		return buildSignedDecoder[int64](typ)
	case reflect.Uint:
		return buildUnsignedDecoder[uint](typ)
	case reflect.Uint8:
		return buildUnsignedDecoder[uint8](typ)
	case reflect.Uint16:
		return buildUnsignedDecoder[uint16](typ)
	case reflect.Uint32:
		return buildUnsignedDecoder[uint32](typ)
	case reflect.Uint64:
		return buildUnsignedDecoder[uint64](typ)
	case reflect.Uintptr:
		return buildUnsignedDecoder[uintptr](typ)
	case reflect.Float32:
		return buildFloatDecoder[float32](typ)
	case reflect.Float64:
		return buildFloatDecoder[float64](typ)
	case reflect.Struct:
		return buildObjectDecoder(typ, visiting)
	case reflect.Array, reflect.Slice:
		return buildSequenceDecoder(typ, visiting)
	case reflect.Map:
		return buildMapDecoder(typ, visiting)
	case reflect.Interface:
		if typ.NumMethod() == 0 {
			return decodeInterface
		}
	}

	return buildStandardDecoder(typ)
}

func buildObjectDecoder(typ reflect.Type, visiting map[reflect.Type]*decodeFunc) decodeFunc {
	resolved := resolveStructFields(typ)
	object := &decodeObject{
		fields: make([]decodeField, 0, len(resolved)),
		exact:  make(map[string]int, len(resolved)),
		typ:    typ,
	}

	for index := range resolved {
		source := &resolved[index]
		parents := make([]reflect.Type, 0, len(source.parents))
		current := typ

		for _, fieldIndex := range source.index[:len(source.index)-1] {
			field := current.Field(fieldIndex)
			current = field.Type

			if current.Kind() == reflect.Pointer {
				if !field.IsExported() {
					return buildStandardDecoder(typ)
				}

				current = current.Elem()
				parents = append(parents, current)
			}
		}

		decode := buildDecoder(source.typ, visiting)

		if hasTagOption(source.opts, "string") && quotedDecodeType(source.typ) {
			if legacyDecodeErrors {
				// Legacy ,string errors can stop the enclosing object early.
				return buildStandardDecoder(typ)
			}

			decode = buildQuotedDecoder(source.typ)
		}

		object.exact[source.name] = len(object.fields)
		object.fields = append(object.fields, decodeField{
			name:    source.name,
			parents: source.parents,
			types:   parents,
			offset:  source.offset,
			decode:  decode,
		})
	}

	return object.decode
}

func buildStandardDecoder(typ reflect.Type) decodeFunc {
	return func(state *decodeState, dst unsafe.Pointer) error {
		start := state.pos

		err := state.skip()
		if err != nil {
			return err
		}

		value := reflect.NewAt(typ, dst).Interface()

		if state.useNumber || state.disallowUnknown {
			decoder := json.NewDecoder(bytes.NewReader(state.data[start:state.pos]))

			if state.useNumber {
				decoder.UseNumber()
			}

			if state.disallowUnknown {
				decoder.DisallowUnknownFields()
			}

			err = decoder.Decode(value)
		} else {
			err = json.Unmarshal(state.data[start:state.pos], value)
		}

		mismatch, ok := errors.AsType[*json.UnmarshalTypeError](err)
		if ok {
			mismatch.Offset += int64(start)
			state.save(mismatch)

			return nil
		}

		if !legacyDecodeErrors {
			state.save(err)

			return nil
		}

		return err
	}
}

func buildQuotedDecoder(typ reflect.Type) decodeFunc {
	fields := []reflect.StructField{{Name: "Value", Type: typ, Tag: `json:",string"`}}
	wrapper := reflect.StructOf(fields)

	return func(state *decodeState, dst unsafe.Pointer) error {
		start := state.pos

		err := state.skip()
		if err != nil {
			return err
		}

		buffer := make([]byte, 0, state.pos-start+10)
		buffer = append(buffer, `{"Value":`...)
		buffer = append(buffer, state.data[start:state.pos]...)
		buffer = append(buffer, '}')

		err = json.Unmarshal(buffer, reflect.NewAt(wrapper, dst).Interface())

		mismatch, ok := errors.AsType[*json.UnmarshalTypeError](err)
		if ok {
			mismatch.Offset += int64(start - 9)
			mismatch.Struct = ""
			mismatch.Field = ""
		}

		state.save(err)

		return nil
	}
}

func quotedDecodeType(typ reflect.Type) bool {
	if typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}

	switch typ.Kind() {
	case reflect.Bool, reflect.String, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr, reflect.Float32, reflect.Float64:
		return true
	default:
		return false
	}
}
