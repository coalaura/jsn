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

const hexTable = "0123456789abcdef"

const (
	opEnc uint8 = iota
	opString
	opBool
	opInt
	opInt64
	opUint
	opUint64
	opFloat64
	opTime
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
	buf     []byte
	wr      io.Writer
	scratch unsafe.Pointer // heap-resident slot for pointer-shaped interface words
}

// TypedEncoder is a pre-compiled encoder bound to T at the type level.
// Encode takes *T directly, so no interface boxing, dynamic type check,
// or per-call heap escape can occur. It is safe for concurrent use by
// multiple Encoders.
type TypedEncoder[T any] struct {
	enc encoderFunc
}

type encoderFunc func(e *Encoder, b []byte, p unsafe.Pointer) ([]byte, error)

type isEmptyFunc func(p unsafe.Pointer) bool

type isZeroer interface {
	IsZero() bool
}

type tapeInstr struct {
	lit      []byte
	enc      encoderFunc
	offset   uintptr
	op       uint8
	indirect bool
}

type tapeEncoder struct {
	instrs []tapeInstr
	tail   []byte
}

type structField struct {
	nameBytes []byte
	enc       encoderFunc
	offset    uintptr
	isEmpty   isEmptyFunc
	omit      bool
	indirect  bool
	op        uint8
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
	safeSet [256]byte
	cache   sync.Map // map[unsafe.Pointer]*cachedEncoder

	timeType            = reflect.TypeFor[time.Time]()
	isZeroerType        = reflect.TypeFor[isZeroer]()
	byteSliceType       = reflect.TypeFor[[]byte]()
	writerMarshalerType = reflect.TypeFor[WriterMarshaler]()
	byteMarshalerType   = reflect.TypeFor[ByteMarshaler]()
)

func init() {
	for i := 0x20; i < 256; i++ {
		safeSet[i] = 1
	}

	safeSet['\\'] = 0
	safeSet['"'] = 0
	safeSet['<'] = 0
	safeSet['>'] = 0
	safeSet['&'] = 0
	// U+2028 and U+2029 start with 0xE2
	safeSet[0xE2] = 0
}

//go:nosplit
func noescape(ptr unsafe.Pointer) unsafe.Pointer {
	uiPtr := uintptr(ptr)

	return unsafe.Pointer(uiPtr ^ 0)
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

func (te *tapeEncoder) encode(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
	instrs := te.instrs

	for i := range instrs {
		ins := &instrs[i]

		b = append(b, ins.lit...)

		fieldPtr := unsafe.Add(ptr, ins.offset)

		if ins.indirect {
			fieldPtr = *(*unsafe.Pointer)(fieldPtr)
			if fieldPtr == nil {
				b = append(b, "null"...)

				continue
			}
		}

		switch ins.op {
		case opString:
			b = writeString(b, *(*string)(fieldPtr))
		case opBool:
			if *(*bool)(fieldPtr) {
				b = append(b, "true"...)
			} else {
				b = append(b, "false"...)
			}
		case opInt:
			b = appendInt64Fast(b, int64(*(*int)(fieldPtr)))
		case opInt64:
			b = appendInt64Fast(b, *(*int64)(fieldPtr))
		case opUint:
			b = strconv.AppendUint(b, uint64(*(*uint)(fieldPtr)), 10)
		case opUint64:
			b = strconv.AppendUint(b, *(*uint64)(fieldPtr), 10)
		case opFloat64:
			f64 := *(*float64)(fieldPtr)

			if math.IsNaN(f64) || math.IsInf(f64, 0) {
				return b, &json.UnsupportedValueError{Value: reflect.ValueOf(f64), Str: strconv.FormatFloat(f64, 'g', -1, 64)}
			}

			b = strconv.AppendFloat(b, f64, 'g', -1, 64)
		case opTime:
			b = append(b, '"')
			b = (*time.Time)(fieldPtr).AppendFormat(b, time.RFC3339Nano)
			b = append(b, '"')
		default:
			var err error

			b, err = ins.enc(enc, b, fieldPtr)
			if err != nil {
				return b, err
			}
		}
	}

	return append(b, te.tail...), nil
}

func buildEncoder(tp reflect.Type, visiting map[reflect.Type]*encoderFunc) encoderFunc {
	if pEnc, ok := visiting[tp]; ok {
		if enc := *pEnc; enc != nil {
			return enc
		}

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
		if scalarOp(tp.Elem()) == opString {
			enc = func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
				s := *(*[]string)(ptr)
				if s == nil {
					return append(b, "null"...), nil
				}

				if len(s) == 0 {
					return append(b, '[', ']'), nil
				}

				b = append(b, '[')
				b = writeString(b, s[0])

				for _, str := range s[1:] {
					b = append(b, ',')
					b = writeString(b, str)
				}

				return append(b, ']'), nil
			}

			*pEnc = enc

			return enc
		}

		if elemTp := tp.Elem(); isFlattenable(elemTp) {
			te := buildTapeEncoder(elemTp, visiting)
			elemSize := elemTp.Size()

			enc = func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
				sh := (*sliceHeader)(ptr)
				if sh.Data == nil {
					return append(b, "null"...), nil
				}

				if sh.Len == 0 {
					return append(b, '[', ']'), nil
				}

				b = append(b, '[')

				var err error

				b, err = te.encode(enc, b, sh.Data)
				if err != nil {
					return b, err
				}

				for i := 1; i < sh.Len; i++ {
					b = append(b, ',')

					b, err = te.encode(enc, b, unsafe.Add(sh.Data, uintptr(i)*elemSize))
					if err != nil {
						return b, err
					}
				}

				return append(b, ']'), nil
			}

			*pEnc = enc

			return enc
		}

		if op := scalarOp(tp.Elem()); op != opEnc {
			elemSize := tp.Elem().Size()

			enc = func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
				sh := (*sliceHeader)(ptr)
				if sh.Data == nil {
					return append(b, "null"...), nil
				}

				if sh.Len == 0 {
					return append(b, '[', ']'), nil
				}

				b = append(b, '[')

				var err error

				b, err = appendScalar(b, op, sh.Data)
				if err != nil {
					return b, err
				}

				for i := 1; i < sh.Len; i++ {
					b = append(b, ',')

					b, err = appendScalar(b, op, unsafe.Add(sh.Data, uintptr(i)*elemSize))
					if err != nil {
						return b, err
					}
				}

				return append(b, ']'), nil
			}

			*pEnc = enc

			return enc
		}

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
		if op := scalarOp(tp.Elem()); op != opEnc {
			elemSize := tp.Elem().Size()
			length := tp.Len()

			enc = func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
				b = append(b, '[')

				var err error

				for i := 0; i < length; i++ {
					if i > 0 {
						b = append(b, ',')
					}

					b, err = appendScalar(b, op, unsafe.Add(ptr, uintptr(i)*elemSize))
					if err != nil {
						return b, err
					}
				}

				return append(b, ']'), nil
			}

			break
		}

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

					mark := len(b)

					for k, val := range m {
						b = append(b, ',')
						b = writeString(b, k)
						b = append(b, ':')
						b = writeString(b, val)
					}

					if len(b) == mark {
						return append(b, '{', '}'), nil
					}

					b[mark] = '{'

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

					mark := len(b)

					for k, val := range m {
						b = append(b, ',')
						b = writeString(b, k)
						b = append(b, ':')
						b = appendInt64Fast(b, val)
					}

					if len(b) == mark {
						return append(b, '{', '}'), nil
					}

					b[mark] = '{'

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

					mark := len(b)

					for k, val := range m {
						b = append(b, ',')
						b = writeString(b, k)
						b = append(b, ':')

						var err error

						b, err = enc.encodeAny(b, val)
						if err != nil {
							return b, err
						}
					}

					if len(b) == mark {
						return append(b, '{', '}'), nil
					}

					b[mark] = '{'

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
		if isFlattenable(tp) {
			enc = buildTapeEncoder(tp, visiting).encode
		} else {
			enc = buildStructEncoder(tp, visiting)
		}
	case reflect.Pointer:
		if pElem, ok := visiting[tp.Elem()]; ok && *pElem == nil {
			enc = func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
				ptrptr := *(*unsafe.Pointer)(ptr)
				if ptrptr == nil {
					return append(b, "null"...), nil
				}

				return (*pElem)(enc, b, ptrptr)
			}

			break
		}

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
			nameBytes: []byte(`,"` + name + `":`),
			enc:       buildEncoder(sf.Type, visiting),
			offset:    sf.Offset,
		}

		field.op, field.indirect = fieldOp(sf.Type)

		omitEmpty := strings.Contains(opts, "omitempty")
		omitZero := strings.Contains(opts, "omitzero")

		switch {
		case omitEmpty && omitZero:
			isEmpty := buildIsEmptyFunc(sf.Type)
			isZero := buildIsZeroFunc(sf.Type)

			field.omit = true
			field.isEmpty = func(p unsafe.Pointer) bool {
				return isEmpty(p) || isZero(p)
			}
		case omitEmpty:
			field.omit = true
			field.isEmpty = buildIsEmptyFunc(sf.Type)
		case omitZero:
			field.omit = true
			field.isEmpty = buildIsZeroFunc(sf.Type)
		}

		fields = append(fields, field)
	}

	return func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
		mark := len(b)

		for i := range fields {
			field := &fields[i]

			fieldPtr := unsafe.Add(ptr, field.offset)

			if field.omit && field.isEmpty(fieldPtr) {
				continue
			}

			b = append(b, field.nameBytes...)

			if field.indirect {
				fieldPtr = *(*unsafe.Pointer)(fieldPtr)
				if fieldPtr == nil {
					b = append(b, "null"...)

					continue
				}
			}

			switch field.op {
			case opString:
				b = writeString(b, *(*string)(fieldPtr))
			case opBool:
				if *(*bool)(fieldPtr) {
					b = append(b, "true"...)
				} else {
					b = append(b, "false"...)
				}
			case opInt:
				b = appendInt64Fast(b, int64(*(*int)(fieldPtr)))
			case opInt64:
				b = appendInt64Fast(b, *(*int64)(fieldPtr))
			case opUint:
				b = strconv.AppendUint(b, uint64(*(*uint)(fieldPtr)), 10)
			case opUint64:
				b = strconv.AppendUint(b, *(*uint64)(fieldPtr), 10)
			case opFloat64:
				f64 := *(*float64)(fieldPtr)

				if math.IsNaN(f64) || math.IsInf(f64, 0) {
					return b, &json.UnsupportedValueError{Value: reflect.ValueOf(f64), Str: strconv.FormatFloat(f64, 'g', -1, 64)}
				}

				b = strconv.AppendFloat(b, f64, 'g', -1, 64)
			case opTime:
				b = append(b, '"')
				b = (*time.Time)(fieldPtr).AppendFormat(b, time.RFC3339Nano)
				b = append(b, '"')
			default:
				var err error

				b, err = field.enc(enc, b, fieldPtr)
				if err != nil {
					return b, err
				}
			}
		}

		if len(b) == mark {
			return append(b, '{', '}'), nil
		}

		b[mark] = '{'

		return append(b, '}'), nil
	}
}

func isFlattenable(tp reflect.Type) bool {
	if tp.Kind() != reflect.Struct || tp == timeType {
		return false
	}

	if tp.Implements(writerMarshalerType) || tp.Implements(byteMarshalerType) {
		return false
	}

	for sf := range tp.Fields() {
		if !sf.IsExported() {
			continue
		}

		tag := sf.Tag.Get("json")
		if tag == "-" {
			continue
		}

		_, opts, _ := strings.Cut(tag, ",")
		if strings.Contains(opts, "omitempty") || strings.Contains(opts, "omitzero") {
			return false
		}
	}

	return true
}

func buildTapeEncoder(tp reflect.Type, visiting map[reflect.Type]*encoderFunc) *tapeEncoder {
	te := &tapeEncoder{}

	var pending []byte

	var walk func(st reflect.Type, base uintptr)

	walk = func(st reflect.Type, base uintptr) {
		pending = append(pending, '{')

		first := true

		for sf := range st.Fields() {
			if !sf.IsExported() {
				continue
			}

			tag := sf.Tag.Get("json")
			if tag == "-" {
				continue
			}

			name, _, _ := strings.Cut(tag, ",")
			if name == "" {
				name = sf.Name
			}

			if !first {
				pending = append(pending, ',')
			}

			first = false

			pending = append(pending, '"')
			pending = append(pending, name...)
			pending = append(pending, '"', ':')

			op, indirect := fieldOp(sf.Type)

			if op == opEnc && !indirect && isFlattenable(sf.Type) {
				walk(sf.Type, base+sf.Offset)

				continue
			}

			if op == opEnc && !indirect && sf.Type.Kind() == reflect.Array && sf.Type.Len() <= 16 {
				if elemOp := scalarOp(sf.Type.Elem()); elemOp != opEnc {
					elemSize := sf.Type.Elem().Size()
					length := sf.Type.Len()

					pending = append(pending, '[')

					for i := 0; i < length; i++ {
						if i > 0 {
							pending = append(pending, ',')
						}

						te.instrs = append(te.instrs, tapeInstr{
							lit:    pending,
							offset: base + sf.Offset + uintptr(i)*elemSize,
							op:     elemOp,
						})

						pending = nil
					}

					pending = append(pending, ']')

					continue
				}
			}

			ins := tapeInstr{
				lit:      pending,
				offset:   base + sf.Offset,
				op:       op,
				indirect: indirect,
			}

			if op == opEnc {
				ins.enc = buildEncoder(sf.Type, visiting)
			}

			te.instrs = append(te.instrs, ins)

			pending = nil
		}

		pending = append(pending, '}')
	}

	walk(tp, 0)

	te.tail = pending

	return te
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

func buildIsZeroFunc(t reflect.Type) isEmptyFunc {
	if t == timeType {
		return func(p unsafe.Pointer) bool {
			return (*time.Time)(p).IsZero()
		}
	}

	if t.Implements(isZeroerType) {
		typPtr := extractTypePtr(t)
		isPtr := isPointerShaped(t.Kind())

		return func(p unsafe.Pointer) bool {
			var val any

			ef := (*eface)(noescape(unsafe.Pointer(&val)))

			ef.typ = typPtr

			if isPtr {
				ef.word = *(*unsafe.Pointer)(p)
				if ef.word == nil {
					return true
				}
			} else {
				ef.word = p
			}

			return val.(interface{ IsZero() bool }).IsZero()
		}
	}

	if ptrType := reflect.PointerTo(t); ptrType.Implements(isZeroerType) {
		typPtr := extractTypePtr(ptrType)

		return func(p unsafe.Pointer) bool {
			var val any

			ef := (*eface)(noescape(unsafe.Pointer(&val)))

			ef.typ = typPtr
			ef.word = p

			return val.(interface{ IsZero() bool }).IsZero()
		}
	}

	switch t.Kind() {
	case reflect.Slice, reflect.Map, reflect.Pointer, reflect.Interface, reflect.Chan, reflect.Func, reflect.UnsafePointer:
		return func(p unsafe.Pointer) bool {
			return *(*unsafe.Pointer)(p) == nil
		}
	case reflect.Struct, reflect.Array, reflect.Complex64, reflect.Complex128:
		return func(p unsafe.Pointer) bool {
			return reflect.NewAt(t, p).Elem().IsZero()
		}
	}

	// remaining scalar kinds: zero and empty coincide
	return buildIsEmptyFunc(t)
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

	var safeEnd int

	inlineLimit := min(length, 48)

	for safeEnd+8 <= inlineLimit {
		if safeSet[str[safeEnd]]&safeSet[str[safeEnd+1]]&safeSet[str[safeEnd+2]]&safeSet[str[safeEnd+3]]&safeSet[str[safeEnd+4]]&safeSet[str[safeEnd+5]]&safeSet[str[safeEnd+6]]&safeSet[str[safeEnd+7]] == 0 {
			break
		}

		safeEnd += 8
	}

	for ; safeEnd < inlineLimit; safeEnd++ {
		if safeSet[str[safeEnd]] == 0 {
			break
		}
	}

	// First 48 bytes are clean and there's more to scan - SIMD the rest.
	if safeEnd == 48 && length > 48 {
		safeEnd += simdFirstEscape(str[48:])
	}

	if safeEnd == length {
		b = append(b, str...)

		return append(b, '"')
	}

	b = append(b, str[:safeEnd]...)

	start := safeEnd

	for i := safeEnd; i < length; i++ {
		ch := str[i]

		if safeSet[ch] != 0 {
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

func fieldOp(t reflect.Type) (uint8, bool) {
	op := scalarOp(t)
	if op != opEnc {
		return op, false
	}

	if t.Kind() == reflect.Pointer && !t.Implements(writerMarshalerType) && !t.Implements(byteMarshalerType) {
		op = scalarOp(t.Elem())
		if op != opEnc {
			return op, true
		}
	}

	return opEnc, false
}

func appendScalar(b []byte, op uint8, p unsafe.Pointer) ([]byte, error) {
	switch op {
	case opString:
		return writeString(b, *(*string)(p)), nil
	case opBool:
		if *(*bool)(p) {
			return append(b, "true"...), nil
		}

		return append(b, "false"...), nil
	case opInt:
		return appendInt64Fast(b, int64(*(*int)(p))), nil
	case opInt64:
		return appendInt64Fast(b, *(*int64)(p)), nil
	case opUint:
		return strconv.AppendUint(b, uint64(*(*uint)(p)), 10), nil
	case opUint64:
		return strconv.AppendUint(b, *(*uint64)(p), 10), nil
	case opFloat64:
		f := *(*float64)(p)

		if math.IsNaN(f) || math.IsInf(f, 0) {
			return b, &json.UnsupportedValueError{Value: reflect.ValueOf(f), Str: strconv.FormatFloat(f, 'g', -1, 64)}
		}

		return strconv.AppendFloat(b, f, 'g', -1, 64), nil
	case opTime:
		b = append(b, '"')
		b = (*time.Time)(p).AppendFormat(b, time.RFC3339Nano)

		return append(b, '"'), nil
	}

	return b, nil
}

func scalarOp(t reflect.Type) uint8 {
	if t == timeType {
		return opTime
	}

	if t == byteSliceType || t.Implements(writerMarshalerType) || t.Implements(byteMarshalerType) {
		return opEnc
	}

	switch t.Kind() {
	case reflect.String:
		return opString
	case reflect.Bool:
		return opBool
	case reflect.Int:
		return opInt
	case reflect.Int64:
		return opInt64
	case reflect.Uint:
		return opUint
	case reflect.Uint64:
		return opUint64
	case reflect.Float64:
		return opFloat64
	}

	return opEnc
}
