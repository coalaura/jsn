package jsn

import (
	"encoding/json"
	"math"
	"reflect"
	"strconv"
	"unsafe"
)

const collectionBatch = 64

type signedScalar interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64
}

type unsignedScalar interface {
	~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~uintptr
}

type floatScalar interface {
	~float32 | ~float64
}

type collectionEncoder func(*Encoder, []byte, unsafe.Pointer, int) ([]byte, error)

func buildScalarCollection(tp reflect.Type) encoderFunc {
	var encode collectionEncoder

	switch scalarOp(tp.Elem()) {
	case opInt:
		encode = encodeSignedCollection[int]
	case opInt8:
		encode = encodeSignedCollection[int8]
	case opInt16:
		encode = encodeSignedCollection[int16]
	case opInt32:
		encode = encodeSignedCollection[int32]
	case opInt64:
		encode = encodeSignedCollection[int64]
	case opUint:
		encode = encodeUnsignedCollection[uint]
	case opUint8:
		encode = encodeUnsignedCollection[uint8]
	case opUint16:
		encode = encodeUnsignedCollection[uint16]
	case opUint32:
		encode = encodeUnsignedCollection[uint32]
	case opUint64:
		encode = encodeUnsignedCollection[uint64]
	case opFloat32:
		encode = encodeFloatCollection[float32]
	case opFloat64:
		encode = encodeFloatCollection[float64]
	case opBool:
		encode = encodeBoolCollection
	default:
		return nil
	}

	if tp.Kind() == reflect.Array {
		length := tp.Len()

		return func(enc *Encoder, buffer []byte, ptr unsafe.Pointer) ([]byte, error) {
			return encode(enc, buffer, ptr, length)
		}
	}

	return func(enc *Encoder, buffer []byte, ptr unsafe.Pointer) ([]byte, error) {
		header := (*sliceHeader)(ptr)
		if header.Data == nil {
			return append(buffer, "null"...), nil
		}

		return encode(enc, buffer, header.Data, header.Len)
	}
}

func encodeSignedCollection[Value signedScalar](enc *Encoder, buffer []byte, ptr unsafe.Pointer, length int) ([]byte, error) {
	values := unsafe.Slice((*Value)(ptr), length)
	buffer = append(buffer, '[')

	for start := 0; start < length; start += collectionBatch {
		end := min(start+collectionBatch, length)
		// A bounded batch amortizes reservations and flush checks without retaining
		// an entire large collection. Each integer needs at most 20 bytes plus ','.
		buffer = reserveBytes(buffer, (end-start)*21+1)

		for index := start; index < end; index++ {
			if index != 0 {
				buffer = append(buffer, ',')
			}

			buffer = appendInt64Fast(buffer, int64(values[index]))
		}

		var err error

		buffer, err = enc.maybeFlush(buffer)
		if err != nil {
			return buffer, err
		}
	}

	return append(buffer, ']'), nil
}

func encodeUnsignedCollection[Value unsignedScalar](enc *Encoder, buffer []byte, ptr unsafe.Pointer, length int) ([]byte, error) {
	values := unsafe.Slice((*Value)(ptr), length)
	buffer = append(buffer, '[')

	for start := 0; start < length; start += collectionBatch {
		end := min(start+collectionBatch, length)
		buffer = reserveBytes(buffer, (end-start)*21+1)

		for index := start; index < end; index++ {
			if index != 0 {
				buffer = append(buffer, ',')
			}

			buffer = appendUint64Fast(buffer, uint64(values[index]))
		}

		var err error

		buffer, err = enc.maybeFlush(buffer)
		if err != nil {
			return buffer, err
		}
	}

	return append(buffer, ']'), nil
}

func encodeFloatCollection[Value floatScalar](enc *Encoder, buffer []byte, ptr unsafe.Pointer, length int) ([]byte, error) {
	values := unsafe.Slice((*Value)(ptr), length)
	buffer = append(buffer, '[')

	for start := 0; start < length; start += collectionBatch {
		end := min(start+collectionBatch, length)
		buffer = reserveBytes(buffer, (end-start)*33+1)

		for index := start; index < end; index++ {
			if index != 0 {
				buffer = append(buffer, ',')
			}

			value := values[index]

			number := float64(value)
			if math.IsNaN(number) || math.IsInf(number, 0) {
				return buffer, &json.UnsupportedValueError{Value: reflect.ValueOf(number), Str: strconv.FormatFloat(number, 'g', -1, int(unsafe.Sizeof(value))*8)}
			}

			// The size is constant in each Go generic instantiation, so no type
			// dispatch remains inside the generated float32 or float64 loop.
			if unsafe.Sizeof(value) == 4 {
				buffer = appendFloat32(buffer, float32(value))
			} else {
				buffer = appendFloat64(buffer, number)
			}
		}

		var err error

		buffer, err = enc.maybeFlush(buffer)
		if err != nil {
			return buffer, err
		}
	}

	return append(buffer, ']'), nil
}

func encodeBoolCollection(enc *Encoder, buffer []byte, ptr unsafe.Pointer, length int) ([]byte, error) {
	values := unsafe.Slice((*bool)(ptr), length)
	buffer = append(buffer, '[')

	for start := 0; start < length; start += collectionBatch {
		end := min(start+collectionBatch, length)
		buffer = reserveBytes(buffer, (end-start)*6+1)

		for index := start; index < end; index++ {
			if index != 0 {
				buffer = append(buffer, ',')
			}

			if values[index] {
				buffer = append(buffer, "true"...)
			} else {
				buffer = append(buffer, "false"...)
			}
		}

		var err error

		buffer, err = enc.maybeFlush(buffer)
		if err != nil {
			return buffer, err
		}
	}

	return append(buffer, ']'), nil
}

func reserveBytes(buffer []byte, extra int) []byte {
	if cap(buffer)-len(buffer) < extra {
		return growBytes(buffer, len(buffer)+extra)
	}

	return buffer
}
