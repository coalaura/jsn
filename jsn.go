package jsn

import (
	"encoding/json"
	"io"
	"maps"
	"math"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
)

const hexTable = "0123456789abcdef"

type WriterMarshaler interface {
	MarshalJSON(io.Writer) error
}

type ByteMarshaler interface {
	MarshalJSON() ([]byte, error)
}

type Encoder struct {
	wr  io.Writer
	buf []byte
}

type encoderFunc func(e *Encoder, v reflect.Value) error

type isEmptyFunc func(v reflect.Value) bool

type structField struct {
	nameBytes []byte
	enc       encoderFunc
	idx       int
	omitEmpty bool
	isEmpty   isEmptyFunc
}

var (
	safeSet [256]bool
	cache   atomic.Pointer[map[reflect.Type]encoderFunc]

	writerMarshalerType = reflect.TypeFor[WriterMarshaler]()
	byteMarshalerType   = reflect.TypeFor[ByteMarshaler]()
)

func init() {
	for i := 0x20; i < 256; i++ {
		safeSet[i] = true
	}

	safeSet['\\'] = false
	safeSet['"'] = false
	safeSet['<'] = false
	safeSet['>'] = false
	safeSet['&'] = false
	// U+2028 and U+2029 start with 0xE2
	safeSet[0xE2] = false
}

func init() {
	m := make(map[reflect.Type]encoderFunc)

	cache.Store(&m)
}

// NewEncoder returns a new JSON encoder writing to wr.
func NewEncoder(wr io.Writer) *Encoder {
	return &Encoder{
		wr:  wr,
		buf: make([]byte, 0, 1024),
	}
}

// Encode marshals v to JSON and writes it to the underlying writer.
func (e *Encoder) Encode(v any) error {
	e.buf = e.buf[:0]

	if err := e.encodeValue(reflect.ValueOf(v)); err != nil {
		return err
	}

	e.buf = append(e.buf, '\n')

	_, err := e.wr.Write(e.buf)
	return err
}

// Write allows the Encoder to satisfy io.Writer for WriterMarshaler.
// It directly appends to the internal buffer, avoiding allocations.
func (e *Encoder) Write(p []byte) (int, error) {
	e.buf = append(e.buf, p...)

	return len(p), nil
}

func (e *Encoder) writeString(s string) {
	e.buf = append(e.buf, '"')

	var start int

	for i := 0; i < len(s); i++ {
		ch := s[i]

		if safeSet[ch] {
			continue
		}

		if ch == 0xE2 && i+2 < len(s) && s[i+1] == 0x80 && (s[i+2] == 0xA8 || s[i+2] == 0xA9) {
			if i > start {
				e.buf = append(e.buf, s[start:i]...)
			}

			e.buf = append(e.buf, '\\', 'u', '2', '0', '2', hexTable[s[i+2]&0xf])

			i += 2
			start = i + 1

			continue
		}

		if ch == 0xE2 {
			// actually a safe character
			continue
		}

		if i > start {
			e.buf = append(e.buf, s[start:i]...)
		}

		switch ch {
		case '\\', '"':
			e.buf = append(e.buf, '\\', ch)
		case '\n':
			e.buf = append(e.buf, '\\', 'n')
		case '\r':
			e.buf = append(e.buf, '\\', 'r')
		case '\t':
			e.buf = append(e.buf, '\\', 't')
		default:
			e.buf = append(e.buf, '\\', 'u', '0', '0', hexTable[ch>>4], hexTable[ch&0xf])
		}

		start = i + 1
	}

	if len(s) > start {
		e.buf = append(e.buf, s[start:]...)
	}

	e.buf = append(e.buf, '"')
}

func (e *Encoder) encodeValue(v reflect.Value) error {
	if !v.IsValid() {
		e.buf = append(e.buf, "null"...)

		return nil
	}

	enc := getEncoder(v.Type())

	return enc(e, v)
}

func getEncoder(tp reflect.Type) encoderFunc {
	m := *cache.Load()

	if fn, ok := m[tp]; ok {
		return fn
	}

	fn := buildEncoder(tp, make(map[reflect.Type]*encoderFunc))

	for {
		mPtr := cache.Load()

		m := *mPtr

		if cached, ok := m[tp]; ok {
			return cached
		}

		newM := make(map[reflect.Type]encoderFunc, len(m)+1)

		maps.Copy(newM, m)

		newM[tp] = fn

		if cache.CompareAndSwap(mPtr, &newM) {
			break
		}
	}

	return fn
}

func buildEncoder(tp reflect.Type, visiting map[reflect.Type]*encoderFunc) encoderFunc {
	// break cyclic structs
	if p, ok := visiting[tp]; ok {
		return func(e *Encoder, v reflect.Value) error {
			return (*p)(e, v)
		}
	}

	var enc encoderFunc

	visiting[tp] = &enc

	if tp.Implements(writerMarshalerType) {
		enc = func(e *Encoder, v reflect.Value) error {
			if v.Kind() == reflect.Pointer && v.IsNil() {
				e.buf = append(e.buf, "null"...)

				return nil
			}

			return v.Interface().(WriterMarshaler).MarshalJSON(e)
		}

		return enc
	}

	if tp.Implements(byteMarshalerType) {
		enc = func(e *Encoder, v reflect.Value) error {
			if v.Kind() == reflect.Pointer && v.IsNil() {
				e.buf = append(e.buf, "null"...)

				return nil
			}

			b, err := v.Interface().(ByteMarshaler).MarshalJSON()
			if err == nil {
				e.buf = append(e.buf, b...)
			}

			return err
		}

		return enc
	}

	switch tp.Kind() {
	case reflect.String:
		enc = func(e *Encoder, v reflect.Value) error {
			e.writeString(v.String())

			return nil
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		enc = func(e *Encoder, v reflect.Value) error {
			e.buf = strconv.AppendInt(e.buf, v.Int(), 10)

			return nil
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		enc = func(e *Encoder, v reflect.Value) error {
			e.buf = strconv.AppendUint(e.buf, v.Uint(), 10)

			return nil
		}
	case reflect.Float32:
		enc = func(e *Encoder, v reflect.Value) error {
			f := v.Float()

			if math.IsNaN(f) || math.IsInf(f, 0) {
				return &json.UnsupportedValueError{Value: v, Str: strconv.FormatFloat(f, 'g', -1, 32)}
			}

			e.buf = strconv.AppendFloat(e.buf, f, 'g', -1, 32)

			return nil
		}
	case reflect.Float64:
		enc = func(e *Encoder, v reflect.Value) error {
			f := v.Float()

			if math.IsNaN(f) || math.IsInf(f, 0) {
				return &json.UnsupportedValueError{Value: v, Str: strconv.FormatFloat(f, 'g', -1, 64)}
			}

			e.buf = strconv.AppendFloat(e.buf, f, 'g', -1, 64)

			return nil
		}
	case reflect.Bool:
		enc = func(e *Encoder, v reflect.Value) error {
			if v.Bool() {
				e.buf = append(e.buf, "true"...)
			} else {
				e.buf = append(e.buf, "false"...)
			}

			return nil
		}
	case reflect.Slice:
		elemEnc := buildEncoder(tp.Elem(), visiting)

		enc = func(e *Encoder, v reflect.Value) error {
			if v.IsNil() {
				e.buf = append(e.buf, "null"...)

				return nil
			}

			e.buf = append(e.buf, '[')

			for i := range v.Len() {
				if i > 0 {
					e.buf = append(e.buf, ',')
				}

				err := elemEnc(e, v.Index(i))
				if err != nil {
					return err
				}
			}

			e.buf = append(e.buf, ']')

			return nil
		}
	case reflect.Map:
		keyEnc := buildEncoder(tp.Key(), visiting)
		valEnc := buildEncoder(tp.Elem(), visiting)

		isStrKey := tp.Key().Kind() == reflect.String

		enc = func(e *Encoder, v reflect.Value) error {
			if v.IsNil() {
				e.buf = append(e.buf, "null"...)

				return nil
			}

			e.buf = append(e.buf, '{')

			first := true

			iter := v.MapRange()

			for iter.Next() {
				if !first {
					e.buf = append(e.buf, ',')
				}

				first = false

				if isStrKey {
					e.writeString(iter.Key().String())
				} else {
					e.buf = append(e.buf, '"')

					err := keyEnc(e, iter.Key())
					if err != nil {
						return err
					}

					e.buf = append(e.buf, '"')
				}

				e.buf = append(e.buf, ':')

				err := valEnc(e, iter.Value())
				if err != nil {
					return err
				}
			}

			e.buf = append(e.buf, '}')

			return nil
		}
	case reflect.Struct:
		enc = buildStructEncoder(tp, visiting)
	case reflect.Pointer:
		elemEnc := buildEncoder(tp.Elem(), visiting)

		enc = func(e *Encoder, v reflect.Value) error {
			if v.IsNil() {
				e.buf = append(e.buf, "null"...)

				return nil
			}

			return elemEnc(e, v.Elem())
		}
	case reflect.Interface:
		enc = func(e *Encoder, v reflect.Value) error {
			if v.IsNil() {
				e.buf = append(e.buf, "null"...)

				return nil
			}

			return e.encodeValue(v.Elem())
		}
	default:
		enc = func(e *Encoder, v reflect.Value) error {
			// fallback for unsupported types (funcs, channels)
			e.buf = append(e.buf, "null"...)

			return nil
		}
	}

	return enc
}

func buildStructEncoder(tp reflect.Type, visiting map[reflect.Type]*encoderFunc) encoderFunc {
	var fields []structField

	for i := range tp.NumField() {
		sf := tp.Field(i)
		if !sf.IsExported() {
			continue
		}

		tag := sf.Tag.Get("json")
		if tag == "-" {
			continue
		}

		name, opts, _ := strings.Cut(tag, ",")
		if name == "" {
			name = sf.Name
		}

		field := structField{
			nameBytes: []byte(`"` + name + `":`),
			enc:       buildEncoder(sf.Type, visiting),
			idx:       i,
			omitEmpty: strings.Contains(opts, "omitempty"),
			isEmpty:   buildIsEmptyFunc(sf.Type),
		}

		fields = append(fields, field)
	}

	return func(e *Encoder, v reflect.Value) error {
		e.buf = append(e.buf, '{')

		first := true

		for _, f := range fields {
			fv := v.Field(f.idx)

			if f.omitEmpty && f.isEmpty(fv) {
				continue
			}

			if !first {
				e.buf = append(e.buf, ',')
			}

			first = false

			e.buf = append(e.buf, f.nameBytes...)

			err := f.enc(e, fv)
			if err != nil {
				return err
			}
		}

		e.buf = append(e.buf, '}')

		return nil
	}
}

func buildIsEmptyFunc(t reflect.Type) isEmptyFunc {
	switch t.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return func(v reflect.Value) bool {
			return v.Len() == 0
		}
	case reflect.Bool:
		return func(v reflect.Value) bool {
			return !v.Bool()
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return func(v reflect.Value) bool {
			return v.Int() == 0
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return func(v reflect.Value) bool {
			return v.Uint() == 0
		}
	case reflect.Float32, reflect.Float64:
		return func(v reflect.Value) bool {
			return v.Float() == 0
		}
	case reflect.Interface, reflect.Pointer:
		return func(v reflect.Value) bool {
			return v.IsNil()
		}
	}

	return func(v reflect.Value) bool {
		return false
	}
}
