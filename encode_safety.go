package jsn

import (
	"bytes"
	"encoding/json"
	"reflect"
	"unsafe"
)

func (enc *Encoder) appendJSON(buffer, raw []byte, typ reflect.Type) ([]byte, error) {
	// Reuse the decoder's allocation-free validator. json.Compact's scanner
	// pool can allocate on every call under the race detector.
	state := decodeState{data: raw}
	err := state.skip()
	state.space()

	if err != nil || state.pos != len(raw) {
		compact := bytes.NewBuffer(enc.jsonScratch[:0])
		err = json.Compact(compact, raw)
		enc.jsonScratch = compact.Bytes()

		if err != nil {
			return buffer, &json.MarshalerError{Type: typ, Err: err}
		}
	} else {
		enc.jsonScratch = appendCompactJSON(enc.jsonScratch[:0], raw)
	}

	output := bytes.NewBuffer(buffer)
	json.HTMLEscape(output, enc.jsonScratch)

	return output.Bytes(), nil
}

func guardCycles(typ reflect.Type, encode encoderFunc) encoderFunc {
	kind := typ.Kind()
	if kind != reflect.Pointer && kind != reflect.Map && kind != reflect.Slice {
		return encode
	}

	return func(enc *Encoder, buffer []byte, ptr unsafe.Pointer) ([]byte, error) {
		visit := encodeVisit{typ: typ, ptr: *(*unsafe.Pointer)(ptr)}
		if visit.ptr == nil {
			return encode(enc, buffer, ptr)
		}

		if kind == reflect.Slice {
			// Different-length views of the same backing array are not cycles.
			visit.length = (*sliceHeader)(ptr).Len
		}

		if _, exists := enc.active[visit]; exists {
			// Snapshot the value before an interface or map scratch slot is reused.
			value := reflect.ValueOf(reflect.NewAt(typ, ptr).Elem().Interface())

			return buffer, &json.UnsupportedValueError{Value: value, Str: "encountered a cycle via " + typ.String()}
		}

		if enc.active == nil {
			enc.active = make(map[encodeVisit]struct{})
		}

		enc.active[visit] = struct{}{}
		defer delete(enc.active, visit)

		return encode(enc, buffer, ptr)
	}
}

func appendCompactJSON(buffer, raw []byte) []byte {
	// Input has already been validated, so strings can be copied verbatim.
	state := decodeState{data: raw}

	for state.pos < len(raw) {
		value := state.space()

		if state.pos == len(raw) {
			break
		}

		start := state.pos

		if value == '"' {
			_, _, _ = state.stringToken()
		} else {
			state.pos++
		}

		buffer = append(buffer, raw[start:state.pos]...)
	}

	return buffer
}
