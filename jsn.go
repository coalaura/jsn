package jsn

import (
	"encoding"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"math"
	"reflect"
	"runtime"
	"strconv"
	"sync/atomic"
	"time"
	"unicode/utf8"
	"unsafe"
)

const hexTable = "0123456789abcdef"

const (
	opEnc uint8 = iota
	opString
	opBool
	opInt
	opInt8
	opInt16
	opInt32
	opInt64
	opUint
	opUint8
	opUint16
	opUint32
	opUint64
	opFloat32
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
	buf            []byte
	wr             io.Writer
	flushThreshold int
	mapScratch     map[reflect.Type]*mapScratchPool

	// scratch holds direct-interface words during encodeAny, including
	// single-field structs and single-element arrays of direct values.
	// Re-entrant safe: the outer call dereferences &scratch before any
	// nested call can overwrite it. scratch is reused across re-entrant
	// encodeAny calls. Pointer-shaped encoders MUST dereference ptr
	// (reading the inner pointer) before making any nested call that
	// could trigger encodeAny.
	scratch unsafe.Pointer
}

// TypedEncoder is a pre-compiled encoder bound to T at the type level.
// Encode takes *T directly, avoiding interface boxing and dynamic type
// checks. For zero allocations, reuse heap-resident values. It is safe
// for concurrent use by multiple Encoders.
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
	parents   []uintptr
	enc       encoderFunc
	offset    uintptr
	isEmpty   isEmptyFunc
	omit      bool
	indirect  bool
	op        uint8
}

// eface mirrors the runtime's two-word interface layout.
// Stable since Go 1.0 but not spec-guaranteed.
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

type cacheMap struct {
	m atomic.Pointer[map[uintptr]*cachedEncoder]
}

var (
	ErrTimeOutOfRange     = errors.New("jsn: time.Time year outside of range [0,9999]")
	ErrTimeZoneOutOfRange = errors.New("jsn: time.Time timezone hour outside of range [0,23]")
)

// safeSet marks ASCII bytes that need neither escaping nor UTF-8 validation.
var safeSet [256]byte

var (
	cache cacheMap

	timeType            = reflect.TypeFor[time.Time]()
	isZeroerType        = reflect.TypeFor[isZeroer]()
	rawMessageType      = reflect.TypeFor[json.RawMessage]()
	writerMarshalerType = reflect.TypeFor[WriterMarshaler]()
	byteMarshalerType   = reflect.TypeFor[ByteMarshaler]()
	textMarshalerType   = reflect.TypeFor[encoding.TextMarshaler]()
	numberType          = reflect.TypeFor[json.Number]()
)

func init() {
	for i := 0x20; i < utf8.RuneSelf; i++ {
		safeSet[i] = 1
	}

	safeSet['\\'] = 0
	safeSet['"'] = 0
	safeSet['<'] = 0
	safeSet['>'] = 0
	safeSet['&'] = 0
}

// CompileTyped builds and caches an encoder for T. Use this on hot paths
// to eliminate runtime reflection and interface boxing overhead. For
// zero allocations, pass heap-resident values (v escapes into the
// compiled encoder).
func CompileTyped[T any]() *TypedEncoder[T] {
	return &TypedEncoder[T]{enc: getEncoder(reflect.TypeFor[T]()).enc}
}

// NewEncoder returns a new Encoder that writes JSON values to wr,
// pre-allocating a 1KB internal buffer. The encoder streams to wr in
// 4KB chunks; wrap wr in a bytes.Buffer if you need full buffering.
func NewEncoder(wr io.Writer) *Encoder {
	return &Encoder{
		wr:             wr,
		buf:            make([]byte, 0, 1024),
		flushThreshold: 4096,
	}
}

func (c *cacheMap) load(key uintptr) (*cachedEncoder, bool) {
	m := c.m.Load()
	if m != nil {
		if v, ok := (*m)[key]; ok {
			return v, true
		}
	}

	return nil, false
}

func (c *cacheMap) loadOrStore(key uintptr, value *cachedEncoder) (*cachedEncoder, bool) {
	v, ok := c.load(key)
	if ok {
		return v, true
	}

	for {
		old := c.m.Load()

		var oldMap map[uintptr]*cachedEncoder

		if old != nil {
			oldMap = *old
		}

		if existing, ok := oldMap[key]; ok {
			return existing, true
		}

		newMap := maps.Clone(oldMap)

		if newMap == nil {
			newMap = make(map[uintptr]*cachedEncoder, 1)
		}

		newMap[key] = value

		if c.m.CompareAndSwap(old, &newMap) {
			return value, false
		}
	}
}

// Encode writes the JSON encoding of *v followed by a newline.
// On error, data already flushed to the underlying writer is not
// rolled back.
func (te *TypedEncoder[T]) Encode(e *Encoder, v *T) error {
	if v == nil {
		e.buf = append(e.buf[:0], "null\n"...)

		return e.Flush()
	}

	b, err := te.enc(e, e.buf[:0], unsafe.Pointer(v))
	e.scratch = nil

	if err != nil {
		e.buf = b

		return err
	}

	e.buf = append(b, '\n')

	return e.Flush()
}

// Encode writes the JSON encoding of val followed by a newline to the
// underlying writer. The dynamic type of val is looked up in the encoder
// cache; for hot paths prefer CompileTyped.
//
// On error, data already flushed to the underlying writer is not
// rolled back.
func (e *Encoder) Encode(val any) error {
	b := e.buf[:0]

	var err error

	b, err = e.encodeAny(b, val)
	e.scratch = nil

	if err != nil {
		e.buf = b

		return err
	}

	e.buf = append(b, '\n')

	return e.Flush()
}

// Write implements io.Writer by appending p to the internal buffer.
// It exists so the Encoder can be passed to WriterMarshaler.MarshalJSONTo
// and never returns an error.
func (e *Encoder) Write(p []byte) (int, error) {
	e.buf = append(e.buf, p...)

	if len(e.buf) >= e.flushThreshold {
		_, err := e.wr.Write(e.buf)
		if err != nil {
			return 0, err
		}

		e.buf = e.buf[:0]
	}

	return len(p), nil
}

// Flush writes any buffered data to the underlying writer.
func (e *Encoder) Flush() error {
	if len(e.buf) > 0 {
		_, err := e.wr.Write(e.buf)

		e.buf = e.buf[:0]

		return err
	}

	return nil
}

func (e *Encoder) maybeFlush(b []byte) ([]byte, error) {
	if len(b) >= e.flushThreshold {
		_, err := e.wr.Write(b)
		if err != nil {
			return b, err
		}

		return b[:0], nil
	}

	return b, nil
}

func growBytes(b []byte, needCap int) []byte {
	newCap := needCap

	double := 2 * cap(b)
	if double > newCap {
		newCap = double
	}

	newB := make([]byte, len(b), newCap)
	copy(newB, b)

	return newB
}

func getEncoder(tp reflect.Type) *cachedEncoder {
	key := uintptr(extractTypePtr(tp))

	ce, ok := cache.load(key)
	if ok {
		return ce
	}

	enc := buildEncoder(tp, make(map[reflect.Type]*encoderFunc))

	ce = &cachedEncoder{
		enc:   enc,
		isPtr: isDirectInterface(tp),
	}

	actual, _ := cache.loadOrStore(key, ce)

	return actual
}

func (e *Encoder) encodeAny(b []byte, val any) ([]byte, error) {
	ef := *(*eface)(unsafe.Pointer(&val))
	if ef.typ == nil {
		return append(b, "null"...), nil
	}

	ce, ok := cache.load(uintptr(ef.typ))
	if !ok {
		ce = getEncoder(reflect.TypeOf(val))
	}

	var ptr unsafe.Pointer

	if ce.isPtr {
		e.scratch = ef.word

		ptr = unsafe.Pointer(&e.scratch)
	} else {
		ptr = ef.word
	}

	b, err := ce.enc(e, b, ptr)

	runtime.KeepAlive(val)

	return b, err
}

func (te *tapeEncoder) encode(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
	instrs := te.instrs

	for i := range instrs {
		if i&63 == 63 {
			var err error

			b, err = enc.maybeFlush(b)
			if err != nil {
				return b, err
			}
		}

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
		case opInt8:
			b = appendInt64Fast(b, int64(*(*int8)(fieldPtr)))
		case opInt16:
			b = appendInt64Fast(b, int64(*(*int16)(fieldPtr)))
		case opInt32:
			b = appendInt64Fast(b, int64(*(*int32)(fieldPtr)))
		case opInt64:
			b = appendInt64Fast(b, *(*int64)(fieldPtr))
		case opUint:
			b = appendUint64Fast(b, uint64(*(*uint)(fieldPtr)))
		case opUint8:
			b = appendUint64Fast(b, uint64(*(*uint8)(fieldPtr)))
		case opUint16:
			b = appendUint64Fast(b, uint64(*(*uint16)(fieldPtr)))
		case opUint32:
			b = appendUint64Fast(b, uint64(*(*uint32)(fieldPtr)))
		case opUint64:
			b = appendUint64Fast(b, *(*uint64)(fieldPtr))
		case opFloat32:
			f32 := *(*float32)(fieldPtr)

			if math.IsNaN(float64(f32)) || math.IsInf(float64(f32), 0) {
				return b, &json.UnsupportedValueError{Value: reflect.ValueOf(float64(f32)), Str: strconv.FormatFloat(float64(f32), 'g', -1, 32)}
			}

			b = appendFloat32(b, f32)
		case opFloat64:
			f64 := *(*float64)(fieldPtr)

			if math.IsNaN(f64) || math.IsInf(f64, 0) {
				return b, &json.UnsupportedValueError{Value: reflect.ValueOf(f64), Str: strconv.FormatFloat(f64, 'g', -1, 64)}
			}

			b = appendFloat64(b, f64)
		case opTime:
			var err error

			b, err = appendTime(b, (*time.Time)(fieldPtr))
			if err != nil {
				return b, err
			}

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
		enc := *pEnc
		if enc != nil {
			return enc
		}

		return func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
			return (*pEnc)(enc, b, ptr)
		}
	}

	pEnc := new(encoderFunc)
	visiting[tp] = pEnc

	typPtr := extractTypePtr(tp)
	isDirect := isDirectInterface(tp)
	isPointer := tp.Kind() == reflect.Pointer

	// Interface values must be unpacked before checking concrete receiver methods.
	if tp.Kind() == reflect.Interface {
		enc := func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
			return enc.encodeAny(b, *(*any)(ptr))
		}

		if tp.NumMethod() != 0 {
			enc = func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
				value := reflect.NewAt(tp, ptr).Elem().Interface()

				return enc.encodeAny(b, value)
			}
		}

		*pEnc = enc

		return enc
	}

	if tp == numberType {
		*pEnc = encodeNumber

		return encodeNumber
	}

	if tp == rawMessageType {
		enc := func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
			s := *(*json.RawMessage)(ptr)
			if s == nil {
				return append(b, "null"...), nil
			}

			return append(b, s...), nil
		}

		*pEnc = enc

		return enc
	}

	// Named byte slices share the base64 path unless a marshaler overrides it.
	isByteSlice := tp.Kind() == reflect.Slice && tp.Elem().Kind() == reflect.Uint8
	if isByteSlice && !implementsAnyMarshaler(tp) && !implementsAnyMarshaler(tp.Elem()) {
		enc := func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
			s := *(*[]byte)(ptr)
			if s == nil {
				return append(b, "null"...), nil
			}

			b = append(b, '"')
			b = base64.StdEncoding.AppendEncode(b, s)
			b = append(b, '"')

			return b, nil
		}

		*pEnc = enc

		return enc
	}

	// bypass time.Time allocations
	if tp == timeType {
		enc := func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
			return appendTime(b, (*time.Time)(ptr))
		}

		*pEnc = enc

		return enc
	}

	ptrTp := reflect.PointerTo(tp)
	ptrTypPtr := extractTypePtr(ptrTp)

	if tp.Implements(writerMarshalerType) || ptrTp.Implements(writerMarshalerType) {
		recvTypPtr := typPtr
		indirectReceiver := isDirect

		if !tp.Implements(writerMarshalerType) {
			recvTypPtr = ptrTypPtr
			indirectReceiver = false
		}

		enc := func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
			if isPointer && *(*unsafe.Pointer)(ptr) == nil {
				return append(b, "null"...), nil
			}

			var val any

			ef := (*eface)(unsafe.Pointer(&val))

			ef.typ = recvTypPtr

			if indirectReceiver {
				ef.word = *(*unsafe.Pointer)(ptr)
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

	if tp.Implements(byteMarshalerType) || ptrTp.Implements(byteMarshalerType) {
		recvTypPtr := typPtr
		indirectReceiver := isDirect

		if !tp.Implements(byteMarshalerType) {
			recvTypPtr = ptrTypPtr
			indirectReceiver = false
		}

		enc := func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
			if isPointer && *(*unsafe.Pointer)(ptr) == nil {
				return append(b, "null"...), nil
			}

			var val any

			ef := (*eface)(unsafe.Pointer(&val))

			ef.typ = recvTypPtr

			if indirectReceiver {
				ef.word = *(*unsafe.Pointer)(ptr)
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

	if tp.Implements(textMarshalerType) || ptrTp.Implements(textMarshalerType) {
		recvTypPtr := typPtr
		indirectReceiver := isDirect

		if !tp.Implements(textMarshalerType) {
			recvTypPtr = ptrTypPtr
			indirectReceiver = false
		}

		enc := func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
			if isPointer && *(*unsafe.Pointer)(ptr) == nil {
				return append(b, "null"...), nil
			}

			var val any

			ef := (*eface)(unsafe.Pointer(&val))
			ef.typ = recvTypPtr

			if indirectReceiver {
				ef.word = *(*unsafe.Pointer)(ptr)
			} else {
				ef.word = ptr
			}

			text, err := val.(encoding.TextMarshaler).MarshalText()
			if err != nil {
				return b, err
			}

			return writeString(b, string(text)), nil
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
			return appendInt64Fast(b, int64(*(*int)(ptr))), nil
		}
	case reflect.Int8:
		enc = func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
			return appendInt64Fast(b, int64(*(*int8)(ptr))), nil
		}
	case reflect.Int16:
		enc = func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
			return appendInt64Fast(b, int64(*(*int16)(ptr))), nil
		}
	case reflect.Int32:
		enc = func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
			return appendInt64Fast(b, int64(*(*int32)(ptr))), nil
		}
	case reflect.Int64:
		enc = func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
			return appendInt64Fast(b, *(*int64)(ptr)), nil
		}
	case reflect.Uint, reflect.Uintptr:
		enc = func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
			return appendUint64Fast(b, uint64(*(*uint)(ptr))), nil
		}
	case reflect.Uint8:
		enc = func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
			return appendUint64Fast(b, uint64(*(*uint8)(ptr))), nil
		}
	case reflect.Uint16:
		enc = func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
			return appendUint64Fast(b, uint64(*(*uint16)(ptr))), nil
		}
	case reflect.Uint32:
		enc = func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
			return appendUint64Fast(b, uint64(*(*uint32)(ptr))), nil
		}
	case reflect.Uint64:
		enc = func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
			return appendUint64Fast(b, *(*uint64)(ptr)), nil
		}
	case reflect.Float32:
		enc = func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
			f32 := *(*float32)(ptr)
			f := float64(f32)

			if math.IsNaN(f) || math.IsInf(f, 0) {
				return b, &json.UnsupportedValueError{Value: reflect.ValueOf(f), Str: strconv.FormatFloat(f, 'g', -1, 32)}
			}

			return appendFloat32(b, f32), nil
		}
	case reflect.Float64:
		enc = func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
			f := *(*float64)(ptr)

			if math.IsNaN(f) || math.IsInf(f, 0) {
				return b, &json.UnsupportedValueError{Value: reflect.ValueOf(f), Str: strconv.FormatFloat(f, 'g', -1, 64)}
			}

			return appendFloat64(b, f), nil
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

				var err error

				for _, str := range s[1:] {
					b = append(b, ',')
					b = writeString(b, str)

					b, err = enc.maybeFlush(b)
					if err != nil {
						return b, err
					}
				}

				return append(b, ']'), nil
			}

			*pEnc = enc

			return enc
		}

		elemTp := tp.Elem()
		if isFlattenable(elemTp) {
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

				b, err = enc.maybeFlush(b)
				if err != nil {
					return b, err
				}

				for i := 1; i < sh.Len; i++ {
					b = append(b, ',')

					b, err = te.encode(enc, b, unsafe.Add(sh.Data, uintptr(i)*elemSize))
					if err != nil {
						return b, err
					}

					b, err = enc.maybeFlush(b)
					if err != nil {
						return b, err
					}
				}

				return append(b, ']'), nil
			}

			*pEnc = enc

			return enc
		}

		op := scalarOp(tp.Elem())
		if op != opEnc {
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

				b, err = enc.maybeFlush(b)
				if err != nil {
					return b, err
				}

				for i := 1; i < sh.Len; i++ {
					b = append(b, ',')

					b, err = appendScalar(b, op, unsafe.Add(sh.Data, uintptr(i)*elemSize))
					if err != nil {
						return b, err
					}

					b, err = enc.maybeFlush(b)
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

			b, err = enc.maybeFlush(b)
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

				b, err = enc.maybeFlush(b)
				if err != nil {
					return b, err
				}
			}

			return append(b, ']'), nil
		}
	case reflect.Array:
		op := scalarOp(tp.Elem())
		if op != opEnc {
			elemSize := tp.Elem().Size()
			length := tp.Len()

			enc = func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
				b = append(b, '[')

				var err error

				for i := range length {
					if i > 0 {
						b = append(b, ',')
					}

					b, err = appendScalar(b, op, unsafe.Add(ptr, uintptr(i)*elemSize))
					if err != nil {
						return b, err
					}

					b, err = enc.maybeFlush(b)
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

			b, err = enc.maybeFlush(b)
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

				b, err = enc.maybeFlush(b)
				if err != nil {
					return b, err
				}
			}

			return append(b, ']'), nil
		}
	case reflect.Map:
		keyKind := tp.Key().Kind()

		validKey := keyKind == reflect.String || keyKind >= reflect.Int && keyKind <= reflect.Int64 || keyKind >= reflect.Uint && keyKind <= reflect.Uintptr || tp.Key().Implements(textMarshalerType)
		if !validKey {
			enc = unsupportedEncoder(tp)

			break
		}

		valueType := tp.Elem()
		simpleValue := !implementsAnyMarshaler(valueType) && valueType != numberType

		if valueType.Kind() == reflect.Interface && valueType.NumMethod() != 0 {
			simpleValue = false
		}

		if keyKind == reflect.String && simpleValue {
			switch tp.Elem().Kind() {
			case reflect.String:
				enc = func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
					m := *(*map[string]string)(ptr)
					if m == nil {
						return append(b, "null"...), nil
					}

					b = append(b, '{')

					first := true

					var err error

					for k, val := range m {
						if !first {
							b = append(b, ',')
						}

						first = false

						b = writeString(b, k)
						b = append(b, ':')
						b = writeString(b, val)

						b, err = enc.maybeFlush(b)
						if err != nil {
							return b, err
						}
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

					var err error

					for k, val := range m {
						if !first {
							b = append(b, ',')
						}

						first = false

						b = writeString(b, k)
						b = append(b, ':')
						b = appendInt64Fast(b, val)

						b, err = enc.maybeFlush(b)
						if err != nil {
							return b, err
						}
					}

					return append(b, '}'), nil
				}

				*pEnc = enc

				return enc
			case reflect.Int:
				enc = func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
					m := *(*map[string]int)(ptr)
					if m == nil {
						return append(b, "null"...), nil
					}

					b = append(b, '{')

					first := true

					var err error

					for k, val := range m {
						if !first {
							b = append(b, ',')
						}

						first = false

						b = writeString(b, k)
						b = append(b, ':')
						b = appendInt64Fast(b, int64(val))

						b, err = enc.maybeFlush(b)
						if err != nil {
							return b, err
						}
					}

					return append(b, '}'), nil
				}

				*pEnc = enc

				return enc
			case reflect.Uint64:
				enc = func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
					m := *(*map[string]uint64)(ptr)
					if m == nil {
						return append(b, "null"...), nil
					}

					b = append(b, '{')

					first := true

					var err error

					for k, val := range m {
						if !first {
							b = append(b, ',')
						}

						first = false

						b = writeString(b, k)
						b = append(b, ':')
						b = appendUint64Fast(b, val)

						b, err = enc.maybeFlush(b)
						if err != nil {
							return b, err
						}
					}

					return append(b, '}'), nil
				}

				*pEnc = enc

				return enc
			case reflect.Float64:
				enc = func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
					m := *(*map[string]float64)(ptr)
					if m == nil {
						return append(b, "null"...), nil
					}

					b = append(b, '{')

					first := true

					var err error

					for k, val := range m {
						if !first {
							b = append(b, ',')
						}

						first = false

						b = writeString(b, k)
						b = append(b, ':')

						if math.IsNaN(val) || math.IsInf(val, 0) {
							return b, &json.UnsupportedValueError{Value: reflect.ValueOf(val), Str: strconv.FormatFloat(val, 'g', -1, 64)}
						}

						b = appendFloat64(b, val)

						b, err = enc.maybeFlush(b)
						if err != nil {
							return b, err
						}
					}

					return append(b, '}'), nil
				}

				*pEnc = enc

				return enc
			case reflect.Bool:
				enc = func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
					m := *(*map[string]bool)(ptr)
					if m == nil {
						return append(b, "null"...), nil
					}

					b = append(b, '{')

					first := true

					var err error

					for k, val := range m {
						if !first {
							b = append(b, ',')
						}

						first = false

						b = writeString(b, k)
						b = append(b, ':')

						if val {
							b = append(b, "true"...)
						} else {
							b = append(b, "false"...)
						}

						b, err = enc.maybeFlush(b)
						if err != nil {
							return b, err
						}
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

					var err error

					for k, val := range m {
						if !first {
							b = append(b, ',')
						}

						first = false

						b = writeString(b, k)
						b = append(b, ':')

						b, err = enc.encodeAny(b, val)
						if err != nil {
							return b, err
						}

						b, err = enc.maybeFlush(b)
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

		enc = buildMapEncoder(tp, visiting)
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
	default:
		enc = unsupportedEncoder(tp)
	}

	*pEnc = enc

	return enc
}

func collectStructFields(tp reflect.Type, fields *[]structField, visiting map[reflect.Type]*encoderFunc) {
	resolved := resolveStructFields(tp)

	for index := range resolved {
		sf := &resolved[index]

		nameBytes := make([]byte, 1, len(sf.name)+4)
		nameBytes[0] = ','
		nameBytes = writeString(nameBytes, sf.name)
		nameBytes = append(nameBytes, ':')

		field := structField{
			nameBytes: nameBytes,
			parents:   sf.parents,
			enc:       buildEncoder(sf.typ, visiting),
			offset:    sf.offset,
		}

		field.op, field.indirect = fieldOp(sf.typ)

		if hasTagOption(sf.opts, "string") {
			quoted := buildQuotedEncoder(sf.typ)
			if quoted != nil {
				field.enc = quoted
				field.op = opEnc
				field.indirect = false
			}
		}

		omitEmpty := hasTagOption(sf.opts, "omitempty")
		omitZero := hasTagOption(sf.opts, "omitzero")

		switch {
		case omitEmpty && omitZero:
			isEmpty := buildIsEmptyFunc(sf.typ)
			isZero := buildIsZeroFunc(sf.typ)

			field.omit = true

			field.isEmpty = func(p unsafe.Pointer) bool {
				return isEmpty(p) || isZero(p)
			}
		case omitEmpty:
			field.omit = true
			field.isEmpty = buildIsEmptyFunc(sf.typ)
		case omitZero:
			field.omit = true
			field.isEmpty = buildIsZeroFunc(sf.typ)
		}

		*fields = append(*fields, field)
	}
}

func buildStructEncoder(tp reflect.Type, visiting map[reflect.Type]*encoderFunc) encoderFunc {
	var fields []structField

	collectStructFields(tp, &fields, visiting)

	return func(enc *Encoder, b []byte, ptr unsafe.Pointer) ([]byte, error) {
		b = append(b, '{')

		first := true

		for i := range fields {
			field := &fields[i]

			fieldPtr := ptr

			for _, offset := range field.parents {
				fieldPtr = *(*unsafe.Pointer)(unsafe.Add(fieldPtr, offset))
				if fieldPtr == nil {
					break
				}
			}

			if fieldPtr == nil {
				continue
			}

			fieldPtr = unsafe.Add(fieldPtr, field.offset)

			if field.omit && field.isEmpty(fieldPtr) {
				continue
			}

			if first {
				b = append(b, field.nameBytes[1:]...)

				first = false
			} else {
				b = append(b, field.nameBytes...)
			}

			if field.indirect {
				fieldPtr = *(*unsafe.Pointer)(fieldPtr)
				if fieldPtr == nil {
					b = append(b, "null"...)

					continue
				}
			}

			var err error

			if field.op == opEnc {
				b, err = field.enc(enc, b, fieldPtr)
			} else {
				b, err = appendScalar(b, field.op, fieldPtr)
			}

			if err != nil {
				return b, err
			}
		}

		return append(b, '}'), nil
	}
}

func isFlattenable(tp reflect.Type) bool {
	if tp.Kind() != reflect.Struct || tp == timeType {
		return false
	}

	if implementsAnyMarshaler(tp) {
		return false
	}

	fields := resolveStructFields(tp)

	for index := range fields {
		field := &fields[index]
		if len(field.parents) != 0 || hasTagOption(field.opts, "omitempty") || hasTagOption(field.opts, "omitzero") || hasTagOption(field.opts, "string") {
			return false
		}
	}

	return true
}

func buildTapeEncoder(tp reflect.Type, visiting map[reflect.Type]*encoderFunc) *tapeEncoder {
	var (
		te         = new(tapeEncoder)
		first      bool
		pending    []byte
		walk       func(st reflect.Type, base uintptr)
		walkFields func(st reflect.Type, base uintptr)
	)

	walkFields = func(st reflect.Type, base uintptr) {
		fields := resolveStructFields(st)

		for index := range fields {
			sf := &fields[index]

			if !first {
				pending = append(pending, ',')
			}

			first = false

			pending = writeString(pending, sf.name)
			pending = append(pending, ':')

			op, indirect := fieldOp(sf.typ)

			if op == opEnc && !indirect && isFlattenable(sf.typ) {
				walk(sf.typ, base+sf.offset)

				first = false

				continue
			}

			if op == opEnc && !indirect && sf.typ.Kind() == reflect.Array && sf.typ.Len() <= 16 && !implementsAnyMarshaler(sf.typ) {
				elemOp := scalarOp(sf.typ.Elem())
				if elemOp != opEnc {
					elemSize := sf.typ.Elem().Size()
					length := sf.typ.Len()

					pending = append(pending, '[')

					for i := range length {
						if i > 0 {
							pending = append(pending, ',')
						}

						te.instrs = append(te.instrs, tapeInstr{
							lit:    pending,
							offset: base + sf.offset + uintptr(i)*elemSize,
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
				offset:   base + sf.offset,
				op:       op,
				indirect: indirect,
			}

			if op == opEnc {
				ins.enc = buildEncoder(sf.typ, visiting)
			}

			te.instrs = append(te.instrs, ins)

			pending = nil
		}
	}

	walk = func(st reflect.Type, base uintptr) {
		pending = append(pending, '{')

		first = true

		walkFields(st, base)

		pending = append(pending, '}')
	}

	walk(tp, 0)

	te.tail = pending

	return te
}

func buildIsEmptyFunc(t reflect.Type) isEmptyFunc {
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
		empty := t.Len() == 0

		return func(p unsafe.Pointer) bool {
			return empty
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
	case reflect.Uint, reflect.Uintptr:
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
		isDirect := isDirectInterface(t)
		isPointer := t.Kind() == reflect.Pointer

		if t.Kind() == reflect.Interface {
			return func(p unsafe.Pointer) bool {
				value := reflect.NewAt(t, p).Elem()
				if value.IsNil() {
					return true
				}

				value = value.Elem()
				if value.Kind() == reflect.Pointer && value.IsNil() {
					return true
				}

				return value.Interface().(isZeroer).IsZero()
			}
		}

		return func(p unsafe.Pointer) bool {
			if isPointer && *(*unsafe.Pointer)(p) == nil {
				return true
			}

			var val any

			ef := (*eface)(unsafe.Pointer(&val))

			ef.typ = typPtr

			if isDirect {
				ef.word = *(*unsafe.Pointer)(p)
			} else {
				ef.word = p
			}

			return val.(interface{ IsZero() bool }).IsZero()
		}
	}

	ptrType := reflect.PointerTo(t)
	if ptrType.Implements(isZeroerType) {
		typPtr := extractTypePtr(ptrType)

		return func(p unsafe.Pointer) bool {
			var val any

			ef := (*eface)(unsafe.Pointer(&val))

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
	return (*eface)(unsafe.Pointer(&typ)).word
}

func isDirectInterface(tp reflect.Type) bool {
	switch tp.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Func, reflect.Chan, reflect.UnsafePointer:
		return true
	case reflect.Array:
		return tp.Len() == 1 && isDirectInterface(tp.Elem())
	case reflect.Struct:
		return tp.NumField() == 1 && isDirectInterface(tp.Field(0).Type)
	}

	return false
}

func implementsAnyMarshaler(t reflect.Type) bool {
	pt := reflect.PointerTo(t)

	return t.Implements(writerMarshalerType) || t.Implements(byteMarshalerType) || t.Implements(textMarshalerType) || pt.Implements(writerMarshalerType) || pt.Implements(byteMarshalerType) || pt.Implements(textMarshalerType)
}

//go:noinline
func writeStringLong(b []byte, str string, safeEnd int) []byte {
	length := len(str)
	pos := safeEnd

	for pos < length {
		remaining := length - pos

		if cap(b) < len(b)+remaining {
			b = growBytes(b, len(b)+remaining)
		}

		n := simdCopySafe(b[len(b):cap(b)], str[pos:])
		b = b[:len(b)+n]

		if n == remaining {
			break
		}

		i := pos + n
		ch := str[i]

		if ch >= utf8.RuneSelf {
			b, pos = appendStringRune(b, str, i)

			continue
		}

		switch ch {
		case '\\', '"':
			b = append(b, '\\', ch)
		case '\n':
			b = append(b, '\\', 'n')
		case '\r':
			b = append(b, '\\', 'r')
		case '\t':
			b = append(b, '\\', 't')
		case '\b':
			b = append(b, '\\', 'b')
		case '\f':
			b = append(b, '\\', 'f')
		default:
			b = append(b, '\\', 'u', '0', '0', hexTable[ch>>4], hexTable[ch&0xf])
		}

		pos = i + 1
	}

	return append(b, '"')
}

func writeString(b []byte, str string) []byte {
	length := len(str)
	if length == 0 {
		return append(b, '"', '"')
	}

	b = append(b, '"')

	var safeEnd int

	inlineLimit := min(length, 48)

	// SWAR causes performance regressions here
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

	if safeEnd == 48 && length > 48 {
		safeEnd += simdFirstEscape(str[48:])
	}

	if safeEnd == length {
		b = append(b, str...)

		return append(b, '"')
	}

	b = append(b, str[:safeEnd]...)

	if length > 128 {
		return writeStringLong(b, str, safeEnd)
	}

	start := safeEnd

	for i := safeEnd; i < length; i++ {
		ch := str[i]

		if safeSet[ch] != 0 {
			continue
		}

		if ch >= utf8.RuneSelf {
			b = append(b, str[start:i]...)
			b, start = appendStringRune(b, str, i)

			i = start - 1

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
		case '\b':
			b = append(b, '\\', 'b')
		case '\f':
			b = append(b, '\\', 'f')
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

func appendStringRune(b []byte, str string, index int) ([]byte, int) {
	value, size := utf8.DecodeRuneInString(str[index:])

	switch {
	case value == utf8.RuneError && size == 1:
		b = append(b, `\ufffd`...)
	case value == '\u2028' || value == '\u2029':
		b = append(b, '\\', 'u', '2', '0', '2', hexTable[value&0xf])
	default:
		b = append(b, str[index:index+size]...)
	}

	return b, index + size
}

func fieldOp(tp reflect.Type) (uint8, bool) {
	op := scalarOp(tp)
	if op != opEnc {
		return op, false
	}

	if tp.Kind() == reflect.Pointer && !implementsAnyMarshaler(tp) {
		op = scalarOp(tp.Elem())
		if op != opEnc {
			return op, true
		}
	}

	return opEnc, false
}

func appendScalar(b []byte, op uint8, ptr unsafe.Pointer) ([]byte, error) {
	switch op {
	case opString:
		return writeString(b, *(*string)(ptr)), nil
	case opBool:
		if *(*bool)(ptr) {
			return append(b, "true"...), nil
		}

		return append(b, "false"...), nil
	case opInt:
		return appendInt64Fast(b, int64(*(*int)(ptr))), nil
	case opInt8:
		return appendInt64Fast(b, int64(*(*int8)(ptr))), nil
	case opInt16:
		return appendInt64Fast(b, int64(*(*int16)(ptr))), nil
	case opInt32:
		return appendInt64Fast(b, int64(*(*int32)(ptr))), nil
	case opInt64:
		return appendInt64Fast(b, *(*int64)(ptr)), nil
	case opUint:
		return appendUint64Fast(b, uint64(*(*uint)(ptr))), nil
	case opUint8:
		return appendUint64Fast(b, uint64(*(*uint8)(ptr))), nil
	case opUint16:
		return appendUint64Fast(b, uint64(*(*uint16)(ptr))), nil
	case opUint32:
		return appendUint64Fast(b, uint64(*(*uint32)(ptr))), nil
	case opUint64:
		return appendUint64Fast(b, *(*uint64)(ptr)), nil
	case opFloat32:
		f32 := *(*float32)(ptr)
		f := float64(f32)

		if math.IsNaN(f) || math.IsInf(f, 0) {
			return b, &json.UnsupportedValueError{Value: reflect.ValueOf(f), Str: strconv.FormatFloat(f, 'g', -1, 32)}
		}

		return appendFloat32(b, f32), nil
	case opFloat64:
		f := *(*float64)(ptr)

		if math.IsNaN(f) || math.IsInf(f, 0) {
			return b, &json.UnsupportedValueError{Value: reflect.ValueOf(f), Str: strconv.FormatFloat(f, 'g', -1, 64)}
		}

		return appendFloat64(b, f), nil
	case opTime:
		return appendTime(b, (*time.Time)(ptr))
	}

	return b, nil
}

func appendFloat64(b []byte, f float64) []byte {
	fmt := byte('f')

	abs := math.Abs(f)
	if abs != 0 {
		if abs < 1e-6 || abs >= 1e21 {
			fmt = 'e'
		}
	}

	b = strconv.AppendFloat(b, f, fmt, -1, 64)

	if fmt == 'e' {
		// clean up e-09 to e-9
		n := len(b)
		if n >= 4 && b[n-4] == 'e' && b[n-3] == '-' && b[n-2] == '0' {
			b[n-2] = b[n-1]
			b = b[:n-1]
		}
	}

	return b
}

func appendFloat32(b []byte, f float32) []byte {
	fmt := byte('f')

	abs := math.Abs(float64(f))
	if abs != 0 {
		if float32(abs) < 1e-6 || float32(abs) >= 1e21 {
			fmt = 'e'
		}
	}

	b = strconv.AppendFloat(b, float64(f), fmt, -1, 32)

	if fmt == 'e' {
		// clean up e-09 to e-9
		n := len(b)
		if n >= 4 && b[n-4] == 'e' && b[n-3] == '-' && b[n-2] == '0' {
			b[n-2] = b[n-1]
			b = b[:n-1]
		}
	}

	return b
}

func appendTime(b []byte, t *time.Time) ([]byte, error) {
	year := t.Year()
	if year < 0 || year >= 10000 {
		return b, ErrTimeOutOfRange
	}

	_, offset := t.Zone()
	if offset/3600 <= -24 || offset/3600 >= 24 {
		return b, ErrTimeZoneOutOfRange
	}

	b = append(b, '"')
	b = t.AppendFormat(b, time.RFC3339Nano)

	return append(b, '"'), nil
}

func scalarOp(tp reflect.Type) uint8 {
	if tp == timeType {
		return opTime
	}

	if tp == rawMessageType || tp == numberType || implementsAnyMarshaler(tp) {
		return opEnc
	}

	switch tp.Kind() {
	case reflect.String:
		return opString
	case reflect.Bool:
		return opBool
	case reflect.Int:
		return opInt
	case reflect.Int8:
		return opInt8
	case reflect.Int16:
		return opInt16
	case reflect.Int32:
		return opInt32
	case reflect.Int64:
		return opInt64
	case reflect.Uint, reflect.Uintptr:
		return opUint
	case reflect.Uint8:
		return opUint8
	case reflect.Uint16:
		return opUint16
	case reflect.Uint32:
		return opUint32
	case reflect.Uint64:
		return opUint64
	case reflect.Float32:
		return opFloat32
	case reflect.Float64:
		return opFloat64
	}

	return opEnc
}
