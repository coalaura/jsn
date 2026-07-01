package jsn

import (
	"encoding/json"
	"io"
	"math"
	"reflect"
	"strconv"
	"strings"
	"sync"
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

type encoderFunc func(e *Encoder, b []byte, v reflect.Value) ([]byte, error)

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
	cache   sync.Map // map[reflect.Type]encoderFunc

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

// NewEncoder returns a new JSON encoder writing to wr.
func NewEncoder(wr io.Writer) *Encoder {
	return &Encoder{
		wr:  wr,
		buf: make([]byte, 0, 1024),
	}
}

// Encode marshals v to JSON and writes it to the underlying writer.
func (e *Encoder) Encode(v any) error {
	b := e.buf[:0]

	var err error

	b, err = e.encodeValue(b, reflect.ValueOf(v))
	if err != nil {
		e.buf = b // preserve state in case of partial writes/errors
		return err
	}

	b = append(b, '\n')
	e.buf = b

	_, err = e.wr.Write(b)
	return err
}

// Write allows the Encoder to satisfy io.Writer for WriterMarshaler.
// It directly appends to the internal buffer, avoiding allocations.
func (e *Encoder) Write(p []byte) (int, error) {
	e.buf = append(e.buf, p...)

	return len(p), nil
}

func writeString(b []byte, s string) []byte {
	b = append(b, '"')

	var start int

	for i := 0; i < len(s); i++ {
		ch := s[i]

		if safeSet[ch] {
			continue
		}

		if ch == 0xE2 && i+2 < len(s) && s[i+1] == 0x80 && (s[i+2] == 0xA8 || s[i+2] == 0xA9) {
			b = append(b, s[start:i]...)
			b = append(b, '\\', 'u', '2', '0', '2', hexTable[s[i+2]&0xf])

			i += 2
			start = i + 1

			continue
		}

		if ch == 0xE2 {
			continue // actually a safe character sequence
		}

		b = append(b, s[start:i]...)

		switch ch {
		case '\\', '"':
			b = append(b, '\\', ch)
		case '\n':
			b = append(b, '\\', 'n')
		case '\r':
			b = append(b, '\\', 'r')
		case '\t':
			b = append(b, '\\', 't')
		default:
			b = append(b, '\\', 'u', '0', '0', hexTable[ch>>4], hexTable[ch&0xf])
		}

		start = i + 1
	}

	if start < len(s) {
		b = append(b, s[start:]...)
	}

	return append(b, '"')
}

func (e *Encoder) encodeValue(b []byte, v reflect.Value) ([]byte, error) {
	if !v.IsValid() {
		return append(b, "null"...), nil
	}

	enc := getEncoder(v.Type())

	return enc(e, b, v)
}

func getEncoder(tp reflect.Type) encoderFunc {
	if v, ok := cache.Load(tp); ok {
		return v.(encoderFunc)
	}

	fn := buildEncoder(tp, make(map[reflect.Type]*encoderFunc))

	v, _ := cache.LoadOrStore(tp, fn)
	return v.(encoderFunc)
}

func buildEncoder(tp reflect.Type, visiting map[reflect.Type]*encoderFunc) encoderFunc {
	// break on cyclic structs
	if p, ok := visiting[tp]; ok {
		return func(e *Encoder, b []byte, v reflect.Value) ([]byte, error) {
			return (*p)(e, b, v)
		}
	}

	p := new(encoderFunc)

	visiting[tp] = p

	var enc encoderFunc

	if tp.Implements(writerMarshalerType) {
		enc = func(e *Encoder, b []byte, v reflect.Value) ([]byte, error) {
			if v.Kind() == reflect.Pointer && v.IsNil() {
				return append(b, "null"...), nil
			}

			e.buf = b

			err := v.Interface().(WriterMarshaler).MarshalJSON(e)
			return e.buf, err
		}

		*p = enc

		return enc
	}

	if tp.Implements(byteMarshalerType) {
		enc = func(e *Encoder, b []byte, v reflect.Value) ([]byte, error) {
			if v.Kind() == reflect.Pointer && v.IsNil() {
				return append(b, "null"...), nil
			}

			out, err := v.Interface().(ByteMarshaler).MarshalJSON()
			if err == nil {
				b = append(b, out...)
			}

			return b, err
		}

		*p = enc

		return enc
	}

	switch tp.Kind() {
	case reflect.String:
		enc = func(e *Encoder, b []byte, v reflect.Value) ([]byte, error) {
			return writeString(b, v.String()), nil
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		enc = func(e *Encoder, b []byte, v reflect.Value) ([]byte, error) {
			return strconv.AppendInt(b, v.Int(), 10), nil
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		enc = func(e *Encoder, b []byte, v reflect.Value) ([]byte, error) {
			return strconv.AppendUint(b, v.Uint(), 10), nil
		}
	case reflect.Float32:
		enc = func(e *Encoder, b []byte, v reflect.Value) ([]byte, error) {
			f := v.Float()

			if math.IsNaN(f) || math.IsInf(f, 0) {
				return b, &json.UnsupportedValueError{Value: v, Str: strconv.FormatFloat(f, 'g', -1, 32)}
			}

			return strconv.AppendFloat(b, f, 'g', -1, 32), nil
		}
	case reflect.Float64:
		enc = func(e *Encoder, b []byte, v reflect.Value) ([]byte, error) {
			f := v.Float()

			if math.IsNaN(f) || math.IsInf(f, 0) {
				return b, &json.UnsupportedValueError{Value: v, Str: strconv.FormatFloat(f, 'g', -1, 64)}
			}

			return strconv.AppendFloat(b, f, 'g', -1, 64), nil
		}
	case reflect.Bool:
		enc = func(e *Encoder, b []byte, v reflect.Value) ([]byte, error) {
			if v.Bool() {
				return append(b, "true"...), nil
			}

			return append(b, "false"...), nil
		}
	case reflect.Slice:
		elemEnc := buildEncoder(tp.Elem(), visiting)

		enc = func(e *Encoder, b []byte, v reflect.Value) ([]byte, error) {
			if v.IsNil() {
				return append(b, "null"...), nil
			}

			b = append(b, '[')

			for i := range v.Len() {
				if i > 0 {
					b = append(b, ',')
				}

				var err error

				b, err = elemEnc(e, b, v.Index(i))
				if err != nil {
					return b, err
				}
			}

			return append(b, ']'), nil
		}
	case reflect.Map:
		keyEnc := buildEncoder(tp.Key(), visiting)
		valEnc := buildEncoder(tp.Elem(), visiting)

		isStrKey := tp.Key().Kind() == reflect.String

		enc = func(e *Encoder, b []byte, v reflect.Value) ([]byte, error) {
			if v.IsNil() {
				return append(b, "null"...), nil
			}

			b = append(b, '{')

			first := true
			iter := v.MapRange()

			for iter.Next() {
				if !first {
					b = append(b, ',')
				}

				first = false

				if isStrKey {
					b = writeString(b, iter.Key().String())
				} else {
					b = append(b, '"')
					var err error
					b, err = keyEnc(e, b, iter.Key())
					if err != nil {
						return b, err
					}
					b = append(b, '"')
				}

				b = append(b, ':')
				var err error
				b, err = valEnc(e, b, iter.Value())
				if err != nil {
					return b, err
				}
			}
			return append(b, '}'), nil
		}
	case reflect.Struct:
		enc = buildStructEncoder(tp, visiting)
	case reflect.Pointer:
		elemEnc := buildEncoder(tp.Elem(), visiting)
		enc = func(e *Encoder, b []byte, v reflect.Value) ([]byte, error) {
			if v.IsNil() {
				return append(b, "null"...), nil
			}
			return elemEnc(e, b, v.Elem())
		}
	case reflect.Interface:
		enc = func(e *Encoder, b []byte, v reflect.Value) ([]byte, error) {
			if v.IsNil() {
				return append(b, "null"...), nil
			}
			return e.encodeValue(b, v.Elem())
		}
	default:
		enc = func(e *Encoder, b []byte, v reflect.Value) ([]byte, error) {
			// fallback for unsupported types (funcs, channels)
			return append(b, "null"...), nil
		}
	}

	*p = enc
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

	return func(e *Encoder, b []byte, v reflect.Value) ([]byte, error) {
		b = append(b, '{')
		first := true

		// Use index over value to avoid copying 48 bytes per field on every iteration
		for i := range fields {
			f := &fields[i]
			fv := v.Field(f.idx)

			if f.omitEmpty && f.isEmpty(fv) {
				continue
			}

			if !first {
				b = append(b, ',')
			}

			first = false

			b = append(b, f.nameBytes...)

			var err error
			b, err = f.enc(e, b, fv)
			if err != nil {
				return b, err
			}
		}

		return append(b, '}'), nil
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
