package jsn

import (
	"encoding/json"
	"reflect"
	"strconv"
	"unsafe"
)

type decodeUnsigned interface {
	~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~uintptr
}

func buildStringDecoder(typ reflect.Type) decodeFunc {
	return func(state *decodeState, dst unsafe.Pointer) error {
		switch state.space() {
		case 'n':
			return state.literal("null")
		case '"':
			raw, simple, err := state.stringToken()
			if err != nil {
				return err
			}

			if !simple || string(raw) != *(*string)(dst) {
				*(*string)(dst) = decodeString(raw, simple)
			}

			return nil
		default:
			return state.mismatch(typ)
		}
	}
}

func buildBoolDecoder(typ reflect.Type) decodeFunc {
	return func(state *decodeState, dst unsafe.Pointer) error {
		switch state.space() {
		case 'n':
			return state.literal("null")
		case 't', 'f':
			value := state.data[state.pos] == 't'
			literal := "false"

			if value {
				literal = "true"
			}

			err := state.literal(literal)
			if err == nil {
				*(*bool)(dst) = value
			}

			return err
		default:
			return state.mismatch(typ)
		}
	}
}

func buildSignedDecoder[Value signedScalar](typ reflect.Type) decodeFunc {
	bits := typ.Bits()
	limit := uint64(1) << (bits - 1)

	return func(state *decodeState, dst unsafe.Pointer) error {
		if state.space() == 'n' {
			return state.literal("null")
		}

		start := state.pos

		if state.pos == len(state.data) || state.data[state.pos] != '-' && (state.data[state.pos] < '0' || state.data[state.pos] > '9') {
			return state.mismatch(typ)
		}

		raw, err := state.numberToken()
		if err != nil {
			return err
		}

		negative := raw[0] == '-'
		digits := raw
		maximum := limit - 1

		if negative {
			digits = raw[1:]
			maximum = limit
		}

		value, ok := parseMagnitude(digits, maximum)
		if !ok {
			state.pos = start

			return state.mismatch(typ)
		}

		if negative {
			*(*Value)(dst) = -Value(value)
		} else {
			*(*Value)(dst) = Value(value)
		}

		return nil
	}
}

func buildUnsignedDecoder[Value decodeUnsigned](typ reflect.Type) decodeFunc {
	maximum := uint64(1)<<typ.Bits() - 1

	return func(state *decodeState, dst unsafe.Pointer) error {
		if state.space() == 'n' {
			return state.literal("null")
		}

		start := state.pos

		if state.pos == len(state.data) || state.data[state.pos] < '0' || state.data[state.pos] > '9' {
			return state.mismatch(typ)
		}

		raw, err := state.numberToken()
		if err != nil {
			return err
		}

		value, ok := parseMagnitude(raw, maximum)
		if !ok {
			state.pos = start

			return state.mismatch(typ)
		}

		*(*Value)(dst) = Value(value)

		return nil
	}
}

func buildFloatDecoder[Value floatScalar](typ reflect.Type) decodeFunc {
	bits := typ.Bits()

	return func(state *decodeState, dst unsafe.Pointer) error {
		if state.space() == 'n' {
			return state.literal("null")
		}

		start := state.pos

		if state.pos == len(state.data) || state.data[state.pos] != '-' && (state.data[state.pos] < '0' || state.data[state.pos] > '9') {
			return state.mismatch(typ)
		}

		raw, err := state.numberToken()
		if err != nil {
			return err
		}

		value, err := strconv.ParseFloat(string(raw), bits)
		if err != nil {
			state.pos = start

			return state.mismatch(typ)
		}

		*(*Value)(dst) = Value(value)

		return nil
	}
}

func decodeNumber(state *decodeState, dst unsafe.Pointer) error {
	if state.space() == 'n' {
		return state.literal("null")
	}

	if state.space() == '"' {
		return buildStandardDecoder(numberType)(state, dst)
	}

	if state.pos == len(state.data) || state.data[state.pos] != '-' && (state.data[state.pos] < '0' || state.data[state.pos] > '9') {
		return state.mismatch(numberType)
	}

	raw, err := state.numberToken()
	if err != nil {
		return err
	}

	*(*json.Number)(dst) = json.Number(raw)

	return nil
}

func parseMagnitude(digits []byte, maximum uint64) (uint64, bool) {
	var value uint64

	cutoff := maximum / 10
	last := maximum % 10

	for _, character := range digits {
		digit := uint64(character - '0')
		if digit > 9 || value > cutoff || value == cutoff && digit > last {
			return 0, false
		}

		value = value*10 + digit
	}

	return value, true
}
