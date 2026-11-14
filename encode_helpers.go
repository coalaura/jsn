package jsn

import (
	"encoding"
	"encoding/json"
	"errors"
	"reflect"
	"unsafe"
)

func unsupportedEncoder(tp reflect.Type) encoderFunc {
	return func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
		return b, &json.UnsupportedTypeError{Type: tp}
	}
}

func encodeNumber(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
	value := *(*string)(ptr)
	if value == "" {
		return append(b, '0'), nil
	}

	if !validNumber(value) {
		return b, errors.New("jsn: invalid json.Number literal " + value)
	}

	return append(b, value...), nil
}

func appendMapKey(b []byte, key reflect.Value) ([]byte, error) {
	if key.Kind() == reflect.String {
		return writeString(b, key.String()), nil
	}

	if key.Type().Implements(textMarshalerType) {
		if key.Kind() == reflect.Pointer && key.IsNil() {
			return append(b, '"', '"'), nil
		}

		text, err := key.Interface().(encoding.TextMarshaler).MarshalText()
		if err != nil {
			return b, err
		}

		return writeString(b, string(text)), nil
	}

	b = append(b, '"')

	if key.Kind() >= reflect.Int && key.Kind() <= reflect.Int64 {
		b = appendInt64Fast(b, key.Int())
	} else {
		b = appendUint64Fast(b, key.Uint())
	}

	return append(b, '"'), nil
}

func buildQuotedEncoder(tp reflect.Type) encoderFunc {
	if implementsAnyMarshaler(tp) {
		return nil
	}

	indirect := tp.Kind() == reflect.Pointer
	if indirect {
		tp = tp.Elem()
	}

	kind := tp.Kind()

	supported := kind == reflect.String || kind == reflect.Bool || kind >= reflect.Int && kind <= reflect.Float64
	if !supported || implementsAnyMarshaler(tp) {
		return nil
	}

	op := scalarOp(tp)
	isNumber := tp == numberType

	return func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
		if indirect {
			ptr = *(*unsafe.Pointer)(ptr)
			if ptr == nil {
				return append(b, "null"...), nil
			}
		}

		start := len(b)

		var err error

		if isNumber {
			b, err = encodeNumber(enc, b, ptr)
		} else {
			b, err = appendScalar(b, op, ptr)
		}

		if err != nil {
			return b, err
		}

		return quoteScalar(b, start), nil
	}
}

func quoteScalar(b []byte, start int) []byte {
	extra := 2

	for _, value := range b[start:] {
		if value == '"' || value == '\\' {
			extra++
		}
	}

	end := len(b)
	if cap(b) < end+extra {
		b = growBytes(b, end+extra)
	}

	// Expand backwards so the encoded scalar can be quoted without a scratch buffer.
	b = b[:end+extra]
	b[len(b)-1] = '"'

	output := len(b) - 2

	for input := end - 1; input >= start; input-- {
		value := b[input]
		b[output] = value
		output--

		if value == '"' || value == '\\' {
			b[output] = '\\'
			output--
		}
	}

	b[start] = '"'

	return b
}

func validNumber(value string) bool {
	index := 0
	if value[index] == '-' {
		index++
		if index == len(value) {
			return false
		}
	}

	switch {
	case value[index] == '0':
		index++
	case value[index] >= '1' && value[index] <= '9':
		for index < len(value) && value[index] >= '0' && value[index] <= '9' {
			index++
		}
	default:
		return false
	}

	if index < len(value) && value[index] == '.' {
		index++
		start := index

		for index < len(value) && value[index] >= '0' && value[index] <= '9' {
			index++
		}

		if index == start {
			return false
		}
	}

	if index < len(value) && (value[index] == 'e' || value[index] == 'E') {
		index++

		if index < len(value) && (value[index] == '+' || value[index] == '-') {
			index++
		}

		start := index

		for index < len(value) && value[index] >= '0' && value[index] <= '9' {
			index++
		}

		if index == start {
			return false
		}
	}

	return index == len(value)
}
