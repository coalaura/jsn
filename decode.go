package jsn

import (
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"sync"
	"unsafe"
)

const maxDecodeDepth = 10000

// TypedDecoder is an immutable, cached decoding plan. It is safe for concurrent
// use with separate destinations and Decoders.
type TypedDecoder[T any] struct {
	decode decodeFunc
}

// Decoder reads consecutive JSON values. Like encoding/json.Decoder, it may
// read beyond the requested value. A Decoder is not safe for concurrent use.
type Decoder struct {
	stream          *json.Decoder
	raw             json.RawMessage
	useNumber       bool
	disallowUnknown bool
}

type decodeFunc func(*decodeState, unsafe.Pointer) error

type decodeState struct {
	data            []byte
	pos             int
	depth           int
	saved           error
	useNumber       bool
	disallowUnknown bool
}

var (
	decodeCache     sync.Map
	errDecodeSyntax = errors.New("jsn: invalid JSON")
)

// Unmarshal decodes one complete JSON value into a non-nil pointer. Strings and
// retained JSON own their storage. As with other decoding errors, malformed
// input may leave the destination partially populated.
func Unmarshal(data []byte, dst any) error {
	value := reflect.ValueOf(dst)
	if !value.IsValid() || value.Kind() != reflect.Pointer || value.IsNil() {
		return &json.InvalidUnmarshalError{Type: reflect.TypeOf(dst)}
	}

	return unmarshalPlan(data, value.UnsafePointer(), getDecoder(value.Type().Elem()), false, false)
}

// CompileTypedDecoder compiles and caches a decoding plan for T.
func CompileTypedDecoder[T any]() *TypedDecoder[T] {
	return &TypedDecoder[T]{decode: getDecoder(reflect.TypeFor[T]())}
}

// NewDecoder returns a buffered JSON stream decoder.
func NewDecoder(reader io.Reader) *Decoder {
	return &Decoder{stream: json.NewDecoder(reader)}
}

// Unmarshal decodes one complete JSON value into dst without dynamic type lookup.
func (typed *TypedDecoder[T]) Unmarshal(data []byte, dst *T) error {
	if dst == nil {
		return &json.InvalidUnmarshalError{Type: reflect.TypeFor[*T]()}
	}

	return unmarshalPlan(data, unsafe.Pointer(dst), typed.decode, false, false)
}

// Decode reads the next JSON value into dst.
func (typed *TypedDecoder[T]) Decode(decoder *Decoder, dst *T) error {
	if legacyDecodeErrors {
		return decoder.Decode(dst)
	}

	err := decoder.stream.Decode(&decoder.raw)
	if err != nil {
		return err
	}

	if dst == nil {
		return &json.InvalidUnmarshalError{Type: reflect.TypeFor[*T]()}
	}

	return unmarshalPlan(decoder.raw, unsafe.Pointer(dst), typed.decode, decoder.useNumber, decoder.disallowUnknown)
}

// Decode reads the next JSON value into dst.
func (decoder *Decoder) Decode(dst any) error {
	if legacyDecodeErrors {
		// Legacy stream error offsets include whitespace consumed during framing.
		return decoder.stream.Decode(dst)
	}

	err := decoder.stream.Decode(&decoder.raw)
	if err != nil {
		return err
	}

	value := reflect.ValueOf(dst)
	if !value.IsValid() || value.Kind() != reflect.Pointer || value.IsNil() {
		return &json.InvalidUnmarshalError{Type: reflect.TypeOf(dst)}
	}

	return unmarshalPlan(decoder.raw, value.UnsafePointer(), getDecoder(value.Type().Elem()), decoder.useNumber, decoder.disallowUnknown)
}

// UseNumber decodes numbers in interface values as json.Number.
func (decoder *Decoder) UseNumber() {
	decoder.useNumber = true
	decoder.stream.UseNumber()
}

// DisallowUnknownFields reports object keys without a matching struct field.
func (decoder *Decoder) DisallowUnknownFields() {
	decoder.disallowUnknown = true
	decoder.stream.DisallowUnknownFields()
}

// Buffered returns the unread portion of the stream buffer.
func (decoder *Decoder) Buffered() io.Reader {
	return decoder.stream.Buffered()
}

// InputOffset reports the end of the most recently returned value or token.
func (decoder *Decoder) InputOffset() int64 {
	return decoder.stream.InputOffset()
}

// More reports whether the current array or object has another element.
func (decoder *Decoder) More() bool {
	return decoder.stream.More()
}

// Token returns the next JSON token, including array and object delimiters.
func (decoder *Decoder) Token() (json.Token, error) {
	return decoder.stream.Token()
}

func (state *decodeState) space() byte {
	for state.pos < len(state.data) {
		value := state.data[state.pos]

		switch value {
		case ' ', '\t', '\r', '\n':
			state.pos++
		default:
			return value
		}
	}

	return 0
}

func (state *decodeState) literal(literal string) error {
	if len(state.data)-state.pos < len(literal) || string(state.data[state.pos:state.pos+len(literal)]) != literal {
		return errDecodeSyntax
	}

	state.pos += len(literal)

	return nil
}

func (state *decodeState) enter() error {
	state.depth++
	if state.depth > maxDecodeDepth {
		return errDecodeSyntax
	}

	state.pos++

	return nil
}

func (state *decodeState) save(err error) {
	if state.saved == nil {
		state.saved = err
	}
}

func getDecoder(typ reflect.Type) decodeFunc {
	cached, ok := decodeCache.Load(typ)
	if ok {
		return cached.(decodeFunc)
	}

	decode := buildDecoder(typ, make(map[reflect.Type]*decodeFunc))
	cached, _ = decodeCache.LoadOrStore(typ, decode)

	return cached.(decodeFunc)
}

func unmarshalPlan(data []byte, dst unsafe.Pointer, decode decodeFunc, useNumber, disallowUnknown bool) error {
	state := decodeState{
		data:            data,
		useNumber:       useNumber,
		disallowUnknown: disallowUnknown,
	}

	state.space()

	err := decode(&state, dst)
	if err == nil && state.space() != 0 {
		err = errDecodeSyntax
	}

	if err == nil && state.pos != len(data) {
		err = errDecodeSyntax
	}

	if errors.Is(err, errDecodeSyntax) {
		// Only the error path invokes the standard scanner, preserving its
		// SyntaxError type and exact offsets without a validation prepass.
		var raw json.RawMessage

		err = json.Unmarshal(data, &raw)
		if err == nil {
			return errDecodeSyntax
		}

		return err
	}

	if err != nil {
		return err
	}

	return state.saved
}
