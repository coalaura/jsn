package jsn

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"
)

const (
	hexTable = "0123456789abcdef"

	lsbMask = 0x0101010101010101
	msbMask = 0x8080808080808080
)

// WriterMarshaler is implemented by types that can marshal themselves
// directly into an io.Writer, avoiding intermediate []byte allocations.
// The encoder passes itself as the writer, so output is appended
// straight into the internal buffer.
type WriterMarshaler interface {
	MarshalJSONTo(io.Writer) error
}

// ByteMarshaler is the standard library-compatible marshaler interface.
// Types implementing it return their own JSON encoding as a byte slice.
// Prefer WriterMarshaler where possible, as this interface forces an
// allocation per call.
type ByteMarshaler interface {
	MarshalJSON() ([]byte, error)
}

// Encoder writes JSON values to an underlying io.Writer. It maintains an
// internal buffer that is reused across Encode calls, so a single Encoder
// produces zero allocations in steady state. An Encoder is not safe for
// concurrent use.
type Encoder struct {
	wr      io.Writer
	buf     []byte
	scratch unsafe.Pointer // heap-resident slot for pointer-shaped interface words
}

// TypeEncoder is a pre-compiled encoder bound to a single concrete type.
// It skips the per-call cache lookup and type inspection that Encode
// performs. Create one with Compile and reuse it; it is safe for
// concurrent use by multiple Encoders.
type TypeEncoder struct {
	typ    reflect.Type
	typPtr unsafe.Pointer
	enc    encoderFunc
	isPtr  bool
}

type encoderFunc func(e *Encoder, b []byte, p unsafe.Pointer) ([]byte, error)

type isEmptyFunc func(p unsafe.Pointer) bool

type structField struct {
	nameBytes []byte
	enc       encoderFunc
	offset    uintptr
	omitEmpty bool
	isEmpty   isEmptyFunc
}

type eface struct {
	typ  unsafe.Pointer
	word unsafe.Pointer
}

type cachedEncoder struct {
	enc   encoderFunc
	isPtr bool
}

type sliceHeader struct {
	Data unsafe.Pointer
	Len  int
	Cap  int
}

var (
	safeSet [256]bool
	cache   sync.Map // map[unsafe.Pointer]*cachedEncoder

	timeType            = reflect.TypeFor[time.Time]()
	byteSliceType       = reflect.TypeFor[[]byte]()
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

//go:nosplit
func noescape(ptr unsafe.Pointer) unsafe.Pointer {
	uiPtr := uintptr(ptr)

	return unsafe.Pointer(uiPtr ^ 0)
}

// TypedEncoder is a pre-compiled encoder bound to T at the type level.
// Encode takes *T directly, so no interface boxing, dynamic type check,
// or per-call heap escape can occur. It is safe for concurrent use by
// multiple Encoders.
type TypedEncoder[T any] struct {
	enc encoderFunc
}

// CompileTyped builds and caches an encoder for T. Prefer it over
// Compile/EncodeAs on hot paths; keep EncodeAs for call sites that are
// genuinely dynamic. For zero allocations, pass heap-resident values
// (v escapes into the compiled encoder).
func CompileTyped[T any]() *TypedEncoder[T] {
	return &TypedEncoder[T]{enc: getEncoder(reflect.TypeFor[T]()).enc}
}

// NewEncoder returns a new Encoder that writes JSON values to wr,
// pre-allocating a 1KB internal buffer.
func NewEncoder(wr io.Writer) *Encoder {
	return &Encoder{
		wr:  wr,
		buf: make([]byte, 0, 1024),
	}
}

// Encode writes the JSON encoding of *v followed by a newline.
func (te *TypedEncoder[T]) Encode(e *Encoder, v *T) error {
	if v == nil {
		b := append(e.buf[:0], "null\n"...)
		e.buf = b

		_, err := e.wr.Write(b)
		return err
	}

	b, err := te.enc(e, e.buf[:0], unsafe.Pointer(v))
	if err != nil {
		e.buf = b

		return err
	}

	b = append(b, '\n')
	e.buf = b

	_, err = e.wr.Write(b)
	return err
}

// Encode writes the JSON encoding of val followed by a newline to the
// underlying writer. The dynamic type of val is looked up in the encoder
// cache; for hot paths prefer EncodeAs with a compiled TypeEncoder.
func (e *Encoder) Encode(val any) error {
	b := e.buf[:0]

	var err error

	b, err = e.encodeAny(b, val)
	if err != nil {
		e.buf = b

		return err
	}

	b = append(b, '\n')
	e.buf = b

	_, err = e.wr.Write(b)
	return err
}

// Write implements io.Writer by appending p to the internal buffer.
// It exists so the Encoder can be passed to WriterMarshaler.MarshalJSONTo
// and never returns an error.
func (e *Encoder) Write(p []byte) (int, error) {
	e.buf = append(e.buf, p...)

	return len(p), nil
}

func getEncoder(tp reflect.Type) *cachedEncoder {
	typPtr := extractTypePtr(tp)
	if val, ok := cache.Load(typPtr); ok {
		return val.(*cachedEncoder)
	}

	enc := buildEncoder(tp, make(map[reflect.Type]*encoderFunc))

	ce := &cachedEncoder{
		enc:   enc,
		isPtr: isPointerShaped(tp.Kind()),
	}

	val, _ := cache.LoadOrStore(typPtr, ce)

	return val.(*cachedEncoder)
}

func (e *Encoder) encodeAny(b []byte, val any) ([]byte, error) {
	ef := *(*eface)(noescape(unsafe.Pointer(&val)))
	if ef.typ == nil {
		return append(b, "null"...), nil
	}

	var ce *cachedEncoder

	if cv, ok := cache.Load(ef.typ); ok {
		ce = cv.(*cachedEncoder)
	} else {
		ce = getEncoder(reflect.TypeOf(val))
	}

	var ptr unsafe.Pointer

	if ce.isPtr {
		e.scratch = ef.word

		ptr = unsafe.Pointer(&e.scratch)
	} else {
		ptr = ef.word
	}

	return ce.enc(e, b, ptr)
}

func buildEncoder(tp reflect.Type, visiting map[reflect.Type]*encoderFunc) encoderFunc {
	if pEnc, ok := visiting[tp]; ok {
		return func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
			return (*pEnc)(enc, b, ptr)
		}
	}

	pEnc := new(encoderFunc)
	visiting[tp] = pEnc

	typPtr := extractTypePtr(tp)
	isPtr := isPointerShaped(tp.Kind())

	// fast path for []byte -> Base64
	if tp == byteSliceType {
		enc := func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
			s := *(*[]byte)(ptr)
			if s == nil {
				return append(b, "null"...), nil
			}

			b = append(b, '"')

			encodedLen := base64.StdEncoding.EncodedLen(len(s))

			b = slices.Grow(b, encodedLen)
			start := len(b)

			b = b[:start+encodedLen]

			base64.StdEncoding.Encode(b[start:], s)

			b = append(b, '"')

			return b, nil
		}
		*pEnc = enc
		return enc
	}

	// bypass time.Time allocations
	if tp == timeType {
		enc := func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
			t := *(*time.Time)(ptr)

			b = append(b, '"')
			b = t.AppendFormat(b, time.RFC3339Nano)
			b = append(b, '"')

			return b, nil
		}

		*pEnc = enc

		return enc
	}

	if tp.Implements(writerMarshalerType) {
		enc := func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
			var val any

			ef := (*eface)(noescape(unsafe.Pointer(&val)))

			ef.typ = typPtr

			if isPtr {
				ef.word = *(*unsafe.Pointer)(ptr)
				if ef.word == nil {
					return append(b, "null"...), nil
				}
			} else {
				ef.word = ptr
			}

			enc.buf = b

			err := val.(WriterMarshaler).MarshalJSONTo(enc)

			return enc.buf, err
		}

		*pEnc = enc

		return enc
	}

	if tp.Implements(byteMarshalerType) {
		enc := func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
			var val any

			ef := (*eface)(noescape(unsafe.Pointer(&val)))

			ef.typ = typPtr

			if isPtr {
				ef.word = *(*unsafe.Pointer)(ptr)
				if ef.word == nil {
					return append(b, "null"...), nil
				}
			} else {
				ef.word = ptr
			}

			out, err := val.(ByteMarshaler).MarshalJSON()
			if err == nil {
				b = append(b, out...)
			}

			return b, err
		}

		*pEnc = enc

		return enc
	}

	var enc encoderFunc

	switch tp.Kind() {
	case reflect.String:
		enc = func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
			return writeString(b, *(*string)(ptr)), nil
		}
	case reflect.Int:
		enc = func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
			return strconv.AppendInt(b, int64(*(*int)(ptr)), 10), nil
		}
	case reflect.Int8:
		enc = func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
			return strconv.AppendInt(b, int64(*(*int8)(ptr)), 10), nil
		}
	case reflect.Int16:
		enc = func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
			return strconv.AppendInt(b, int64(*(*int16)(ptr)), 10), nil
		}
	case reflect.Int32:
		enc = func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
			return strconv.AppendInt(b, int64(*(*int32)(ptr)), 10), nil
		}
	case reflect.Int64:
		enc = func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
			return strconv.AppendInt(b, *(*int64)(ptr), 10), nil
		}
	case reflect.Uint:
		enc = func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
			return strconv.AppendUint(b, uint64(*(*uint)(ptr)), 10), nil
		}
	case reflect.Uint8:
		enc = func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
			return strconv.AppendUint(b, uint64(*(*uint8)(ptr)), 10), nil
		}
	case reflect.Uint16:
		enc = func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
			return strconv.AppendUint(b, uint64(*(*uint16)(ptr)), 10), nil
		}
	case reflect.Uint32:
		enc = func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
			return strconv.AppendUint(b, uint64(*(*uint32)(ptr)), 10), nil
		}
	case reflect.Uint64:
		enc = func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
			return strconv.AppendUint(b, *(*uint64)(ptr), 10), nil
		}
	case reflect.Float32:
		enc = func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
			f := float64(*(*float32)(ptr))

			if math.IsNaN(f) || math.IsInf(f, 0) {
				return b, &json.UnsupportedValueError{Value: reflect.ValueOf(f), Str: strconv.FormatFloat(f, 'g', -1, 32)}
			}

			return strconv.AppendFloat(b, f, 'g', -1, 32), nil
		}
	case reflect.Float64:
		enc = func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
			f := *(*float64)(ptr)

			if math.IsNaN(f) || math.IsInf(f, 0) {
				return b, &json.UnsupportedValueError{Value: reflect.ValueOf(f), Str: strconv.FormatFloat(f, 'g', -1, 64)}
			}

			return strconv.AppendFloat(b, f, 'g', -1, 64), nil
		}
	case reflect.Bool:
		enc = func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
			if *(*bool)(ptr) {
				return append(b, "true"...), nil
			}

			return append(b, "false"...), nil
		}
	case reflect.Slice:
		elemEnc := buildEncoder(tp.Elem(), visiting)
		elemSize := tp.Elem().Size()

		enc = func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
			sh := (*sliceHeader)(ptr)
			if sh.Data == nil {
				return append(b, "null"...), nil
			}

			b = append(b, '[')

			if sh.Len == 0 {
				return append(b, ']'), nil
			}

			var err error

			b, err = elemEnc(enc, b, sh.Data)
			if err != nil {
				return b, err
			}

			for i := 1; i < sh.Len; i++ {
				b = append(b, ',')

				elemPtr := unsafe.Add(sh.Data, uintptr(i)*elemSize)

				b, err = elemEnc(enc, b, elemPtr)
				if err != nil {
					return b, err
				}
			}

			return append(b, ']'), nil
		}
	case reflect.Array:
		elemEnc := buildEncoder(tp.Elem(), visiting)
		elemSize := tp.Elem().Size()
		length := tp.Len()

		enc = func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
			b = append(b, '[')
			if length == 0 {
				return append(b, ']'), nil
			}

			var err error

			b, err = elemEnc(enc, b, ptr)
			if err != nil {
				return b, err
			}

			for i := 1; i < length; i++ {
				b = append(b, ',')

				elemPtr := unsafe.Add(ptr, uintptr(i)*elemSize)

				b, err = elemEnc(enc, b, elemPtr)
				if err != nil {
					return b, err
				}
			}

			return append(b, ']'), nil
		}
	case reflect.Map:
		if tp.Key().Kind() == reflect.String {
			switch tp.Elem().Kind() {
			case reflect.String:
				enc = func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
					m := *(*map[string]string)(ptr)
					if m == nil {
						return append(b, "null"...), nil
					}

					b = append(b, '{')

					first := true

					for k, val := range m {
						if !first {
							b = append(b, ',')
						}

						first = false

						b = writeString(b, k)
						b = append(b, ':')
						b = writeString(b, val)
					}

					return append(b, '}'), nil
				}

				*pEnc = enc

				return enc
			case reflect.Int64:
				enc = func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
					m := *(*map[string]int64)(ptr)
					if m == nil {
						return append(b, "null"...), nil
					}

					b = append(b, '{')

					first := true

					for k, val := range m {
						if !first {
							b = append(b, ',')
						}

						first = false

						b = writeString(b, k)
						b = append(b, ':')
						b = strconv.AppendInt(b, val, 10)
					}

					return append(b, '}'), nil
				}

				*pEnc = enc

				return enc
			case reflect.Interface:
				enc = func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
					m := *(*map[string]any)(ptr)
					if m == nil {
						return append(b, "null"...), nil
					}

					b = append(b, '{')

					first := true

					for k, val := range m {
						if !first {
							b = append(b, ',')
						}

						first = false

						b = writeString(b, k)
						b = append(b, ':')

						var err error

						b, err = enc.encodeAny(b, val)
						if err != nil {
							return b, err
						}
					}

					return append(b, '}'), nil
				}

				*pEnc = enc

				return enc
			}
		}

		isStrKey := tp.Key().Kind() == reflect.String

		enc = func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
			rv := reflect.NewAt(tp, ptr).Elem()
			if rv.IsNil() {
				return append(b, "null"...), nil
			}

			b = append(b, '{')

			first := true

			iter := rv.MapRange() // heavy fallback :(

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

					b, err = enc.encodeAny(b, iter.Key().Interface())
					if err != nil {
						return b, err
					}

					b = append(b, '"')
				}

				b = append(b, ':')

				var err error

				b, err = enc.encodeAny(b, iter.Value().Interface())
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

		enc = func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
			ptrptr := *(*unsafe.Pointer)(ptr)
			if ptrptr == nil {
				return append(b, "null"...), nil
			}

			return elemEnc(enc, b, ptrptr)
		}
	case reflect.Interface:
		enc = func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
			v := *(*any)(ptr)

			return enc.encodeAny(b, v)
		}
	default:
		enc = func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
			return append(b, "null"...), nil
		}
	}

	*pEnc = enc

	return enc
}

func buildStructEncoder(tp reflect.Type, visiting map[reflect.Type]*encoderFunc) encoderFunc {
	var fields []structField

	for sf := range tp.Fields() {
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
			offset:    sf.Offset,
			omitEmpty: strings.Contains(opts, "omitempty"),
			isEmpty:   buildIsEmptyFunc(sf.Type),
		}

		fields = append(fields, field)
	}

	return func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
		b = append(b, '{')

		first := true

		for i := range fields {
			f := &fields[i]

			fieldPtr := unsafe.Add(ptr, f.offset)

			if f.omitEmpty && f.isEmpty(fieldPtr) {
				continue
			}

			if !first {
				b = append(b, ',')
			}

			first = false

			b = append(b, f.nameBytes...)

			var err error

			b, err = f.enc(enc, b, fieldPtr)
			if err != nil {
				return b, err
			}
		}

		return append(b, '}'), nil
	}
}

func buildIsEmptyFunc(t reflect.Type) isEmptyFunc {
	if t == timeType {
		return func(p unsafe.Pointer) bool {
			return (*time.Time)(p).IsZero()
		}
	}

	switch t.Kind() {
	case reflect.String:
		return func(p unsafe.Pointer) bool {
			return len(*(*string)(p)) == 0
		}
	case reflect.Slice:
		return func(p unsafe.Pointer) bool {
			return (*sliceHeader)(p).Len == 0
		}
	case reflect.Map:
		return func(p unsafe.Pointer) bool {
			if *(*unsafe.Pointer)(p) == nil {
				return true
			}

			return reflect.NewAt(t, p).Elem().Len() == 0
		}
	case reflect.Array:
		return func(p unsafe.Pointer) bool {
			return reflect.NewAt(t, p).Elem().IsZero()
		}
	case reflect.Bool:
		return func(p unsafe.Pointer) bool {
			return !*(*bool)(p)
		}
	case reflect.Int:
		return func(p unsafe.Pointer) bool {
			return *(*int)(p) == 0
		}
	case reflect.Int8:
		return func(p unsafe.Pointer) bool {
			return *(*int8)(p) == 0
		}
	case reflect.Int16:
		return func(p unsafe.Pointer) bool {
			return *(*int16)(p) == 0
		}
	case reflect.Int32:
		return func(p unsafe.Pointer) bool {
			return *(*int32)(p) == 0
		}
	case reflect.Int64:
		return func(p unsafe.Pointer) bool {
			return *(*int64)(p) == 0
		}
	case reflect.Uint:
		return func(p unsafe.Pointer) bool {
			return *(*uint)(p) == 0
		}
	case reflect.Uint8:
		return func(p unsafe.Pointer) bool {
			return *(*uint8)(p) == 0
		}
	case reflect.Uint16:
		return func(p unsafe.Pointer) bool {
			return *(*uint16)(p) == 0
		}
	case reflect.Uint32:
		return func(p unsafe.Pointer) bool {
			return *(*uint32)(p) == 0
		}
	case reflect.Uint64:
		return func(p unsafe.Pointer) bool {
			return *(*uint64)(p) == 0
		}
	case reflect.Float32:
		return func(p unsafe.Pointer) bool {
			return *(*float32)(p) == 0
		}
	case reflect.Float64:
		return func(p unsafe.Pointer) bool {
			return *(*float64)(p) == 0
		}
	case reflect.Interface, reflect.Pointer:
		return func(p unsafe.Pointer) bool {
			return *(*unsafe.Pointer)(p) == nil
		}
	}

	return func(p unsafe.Pointer) bool {
		return false
	}
}

func extractTypePtr(typ reflect.Type) unsafe.Pointer {
	return (*eface)(noescape(unsafe.Pointer(&typ))).word
}

func isPointerShaped(k reflect.Kind) bool {
	switch k {
	case reflect.Pointer, reflect.Map, reflect.Func, reflect.Chan, reflect.UnsafePointer:
		return true
	}

	return false
}

func writeString(b []byte, str string) []byte {
	length := len(str)
	if length == 0 {
		return append(b, '"', '"')
	}

	b = append(b, '"')

	// BCE
	_ = str[length-1]

	var safeEnd int

	for ; safeEnd < length; safeEnd++ {
		if !safeSet[str[safeEnd]] {
			break
		}
	}

	if safeEnd == length {
		b = append(b, str...)

		return append(b, '"')
	}

	b = append(b, str[:safeEnd]...)

	start := safeEnd

	for i := safeEnd; i < length; i++ {
		ch := str[i]

		if safeSet[ch] {
			continue
		}

		if ch == 0xE2 && i+2 < length && str[i+1] == 0x80 && (str[i+2] == 0xA8 || str[i+2] == 0xA9) {
			b = append(b, str[start:i]...)
			b = append(b, '\\', 'u', '2', '0', '2', hexTable[str[i+2]&0xf])

			i += 2
			start = i + 1

			continue
		}

		if ch == 0xE2 {
			continue
		}

		b = append(b, str[start:i]...)

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

	if start < length {
		b = append(b, str[start:]...)
	}

	return append(b, '"')
}
