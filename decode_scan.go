package jsn

import (
	"bytes"
	"encoding/binary"
	"unicode/utf16"
	"unicode/utf8"
	"unsafe"
)

// stringToken validates escapes and controls, leaving UTF-8 replacement to
// decodeString. The returned bytes alias the input and never escape a lookup.
func (state *decodeState) stringToken() ([]byte, bool, error) {
	if state.pos >= len(state.data) || state.data[state.pos] != '"' {
		return nil, false, errDecodeSyntax
	}

	state.pos++
	start := state.pos
	simple := true

	for state.pos < len(state.data) {
		// Word-at-a-time ASCII scanning is bounded by the input slice. A
		// conservative match falls through to the byte checks below.
		for len(state.data)-state.pos >= 8 {
			word := binary.LittleEndian.Uint64(state.data[state.pos:])

			quote := word ^ 0x2222222222222222
			slash := word ^ 0x5c5c5c5c5c5c5c5c

			matches := (quote - 0x0101010101010101) & ^quote
			matches |= (slash - 0x0101010101010101) & ^slash
			matches |= (word - 0x2020202020202020) & ^word
			matches |= word

			if matches&0x8080808080808080 != 0 {
				break
			}

			state.pos += 8
		}

		if state.pos == len(state.data) {
			break
		}

		value := state.data[state.pos]
		state.pos++

		switch {
		case value == '"':
			return state.data[start : state.pos-1], simple, nil
		case value == '\\':
			simple = false

			if state.pos == len(state.data) {
				return nil, false, errDecodeSyntax
			}

			escape := state.data[state.pos]
			state.pos++

			switch escape {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
			case 'u':
				if len(state.data)-state.pos < 4 || decodeHex(state.data[state.pos:state.pos+4]) < 0 {
					return nil, false, errDecodeSyntax
				}

				state.pos += 4
			default:
				return nil, false, errDecodeSyntax
			}
		case value < 0x20:
			return nil, false, errDecodeSyntax
		case value >= utf8.RuneSelf:
			simple = false
		}
	}

	return nil, false, errDecodeSyntax
}

func (state *decodeState) numberToken() ([]byte, error) {
	start := state.pos

	if state.pos < len(state.data) && state.data[state.pos] == '-' {
		state.pos++
	}

	if state.pos == len(state.data) {
		return nil, errDecodeSyntax
	}

	if state.data[state.pos] == '0' {
		state.pos++
	} else {
		if state.data[state.pos] < '1' || state.data[state.pos] > '9' {
			return nil, errDecodeSyntax
		}

		for state.pos < len(state.data) && state.data[state.pos] >= '0' && state.data[state.pos] <= '9' {
			state.pos++
		}
	}

	if state.pos < len(state.data) && state.data[state.pos] == '.' {
		state.pos++
		fraction := state.pos

		for state.pos < len(state.data) && state.data[state.pos] >= '0' && state.data[state.pos] <= '9' {
			state.pos++
		}

		if state.pos == fraction {
			return nil, errDecodeSyntax
		}
	}

	if state.pos < len(state.data) && (state.data[state.pos] == 'e' || state.data[state.pos] == 'E') {
		state.pos++
		if state.pos < len(state.data) && (state.data[state.pos] == '+' || state.data[state.pos] == '-') {
			state.pos++
		}

		exponent := state.pos

		for state.pos < len(state.data) && state.data[state.pos] >= '0' && state.data[state.pos] <= '9' {
			state.pos++
		}

		if state.pos == exponent {
			return nil, errDecodeSyntax
		}
	}

	return state.data[start:state.pos], nil
}

func (state *decodeState) skip() error {
	switch state.space() {
	case '"':
		_, _, err := state.stringToken()

		return err
	case 'n':
		return state.literal("null")
	case 't':
		return state.literal("true")
	case 'f':
		return state.literal("false")
	case '{', '[':
		object := state.data[state.pos] == '{'
		closeByte := byte(']')

		if object {
			closeByte = '}'
		}

		err := state.enter()
		if err != nil {
			return err
		}

		if state.space() == closeByte {
			state.pos++
			state.depth--

			return nil
		}

		for {
			if object {
				_, _, err = state.stringToken()
				if err != nil {
					return err
				}

				if state.space() != ':' {
					return errDecodeSyntax
				}

				state.pos++
			}

			err = state.skip()
			if err != nil {
				return err
			}

			switch state.space() {
			case closeByte:
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
	default:
		_, err := state.numberToken()

		return err
	}
}

func decodeString(raw []byte, simple bool) string {
	if simple {
		return string(raw)
	}

	validUTF8 := utf8.Valid(raw)
	if validUTF8 && !bytes.ContainsRune(raw, '\\') {
		return string(raw)
	}

	buffer := make([]byte, 0, len(raw))

	for index := 0; index < len(raw); {
		if validUTF8 {
			// Copy ordinary UTF-8 spans in bulk; only escaped bytes need
			// individual decoding. Invalid UTF-8 uses rune replacement below.
			length := bytes.IndexByte(raw[index:], '\\')
			if length < 0 {
				buffer = append(buffer, raw[index:]...)

				break
			}

			buffer = append(buffer, raw[index:index+length]...)
			index += length
		}

		value := raw[index]
		index++

		if value >= utf8.RuneSelf {
			decoded, size := utf8.DecodeRune(raw[index-1:])
			buffer = utf8.AppendRune(buffer, decoded)
			index += size - 1

			continue
		}

		if value != '\\' {
			buffer = append(buffer, value)

			continue
		}

		escape := raw[index]
		index++

		switch escape {
		case '"', '\\', '/':
			buffer = append(buffer, escape)
		case 'b':
			buffer = append(buffer, '\b')
		case 'f':
			buffer = append(buffer, '\f')
		case 'n':
			buffer = append(buffer, '\n')
		case 'r':
			buffer = append(buffer, '\r')
		case 't':
			buffer = append(buffer, '\t')
		case 'u':
			decoded := decodeHex(raw[index : index+4])
			index += 4

			if utf16.IsSurrogate(decoded) {
				if len(raw)-index >= 6 && raw[index] == '\\' && raw[index+1] == 'u' {
					second := decodeHex(raw[index+2 : index+6])

					pair := utf16.DecodeRune(decoded, second)
					if pair != utf8.RuneError {
						decoded = pair
						index += 6
					} else {
						decoded = utf8.RuneError
					}
				} else {
					decoded = utf8.RuneError
				}
			}

			buffer = utf8.AppendRune(buffer, decoded)
		}
	}

	// This fresh allocation is transferred exclusively to the returned
	// string. No mutable view of buffer is retained or reused.
	return unsafe.String(unsafe.SliceData(buffer), len(buffer))
}

func decodeHex(raw []byte) rune {
	var result rune

	for _, value := range raw {
		result <<= 4

		switch {
		case value >= '0' && value <= '9':
			result |= rune(value - '0')
		case value >= 'a' && value <= 'f':
			result |= rune(value - 'a' + 10)
		case value >= 'A' && value <= 'F':
			result |= rune(value - 'A' + 10)
		default:
			return -1
		}
	}

	return result
}
