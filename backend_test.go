package jsn

import (
	"bytes"
	"errors"
	"io"
	"math"
	"reflect"
	"testing"
)

type scalarLimits struct {
	Signed     int
	Signed8    int8
	Signed16   int16
	Signed32   int32
	Signed64   int64
	Unsigned   uint
	Unsigned8  uint8
	Unsigned16 uint16
	Unsigned32 uint32
	Unsigned64 uint64
	Pointer    uintptr
	True       bool
	False      bool
	Array      [3]int64
}

type nestedScalarLimits struct {
	Before bool
	Value  scalarLimits
	After  bool
}

type backendWriter struct {
	writes int
	err    error
}

func (writer *backendWriter) Write(data []byte) (int, error) {
	writer.writes++
	if writer.err != nil {
		return 0, writer.err
	}

	return len(data), nil
}

func TestScalarCollections(t *testing.T) {
	checkScalarCollection(t, []int{math.MinInt, -1, 0, 1, math.MaxInt})
	checkScalarCollection(t, []int8{math.MinInt8, -1, 0, 1, math.MaxInt8})
	checkScalarCollection(t, []int16{math.MinInt16, -1, 0, 1, math.MaxInt16})
	checkScalarCollection(t, []int32{math.MinInt32, -1, 0, 1, math.MaxInt32})
	checkScalarCollection(t, []int64{math.MinInt64, -1, 0, 1, math.MaxInt64})
	checkScalarCollection(t, []uint{0, 1, math.MaxUint})
	checkScalarCollection(t, []uint8{0, 1, math.MaxUint8})
	checkScalarCollection(t, []uint16{0, 1, math.MaxUint16})
	checkScalarCollection(t, []uint32{0, 1, math.MaxUint32})
	checkScalarCollection(t, []uint64{0, 1, math.MaxUint64})
	checkScalarCollection(t, []uintptr{0, 1, ^uintptr(0)})
	checkScalarCollection(t, []float32{-math.MaxFloat32, 0, math.SmallestNonzeroFloat32, 1e-7, 1e-6, 1e20, 1e21, math.MaxFloat32})
	checkScalarCollection(t, []float64{-math.MaxFloat64, math.Copysign(0, -1), math.SmallestNonzeroFloat64, 1e-7, 1e-6, 1e20, 1e21, math.MaxFloat64})
	checkScalarCollection(t, []bool{false, true})
	checkEncoding(t, []marshaledInt{0, 1, 2})
}

func TestScalarCollectionErrors(t *testing.T) {
	values := []any{
		[]float32{1, float32(math.NaN())},
		[]float64{1, math.Inf(1)},
		[65]float32{64: float32(math.Inf(-1))},
		[65]float64{64: math.NaN()},
	}

	for _, value := range values {
		err := NewEncoder(io.Discard).Encode(value)
		if err == nil {
			t.Fatalf("accepted non-finite number in %T", value)
		}
	}
}

func TestFixedRecords(t *testing.T) {
	value := scalarLimits{
		Signed:     math.MinInt,
		Signed8:    math.MinInt8,
		Signed16:   math.MinInt16,
		Signed32:   math.MinInt32,
		Signed64:   math.MinInt64,
		Unsigned:   math.MaxUint,
		Unsigned8:  math.MaxUint8,
		Unsigned16: math.MaxUint16,
		Unsigned32: math.MaxUint32,
		Unsigned64: math.MaxUint64,
		Pointer:    ^uintptr(0),
		True:       true,
		False:      false,
		Array:      [3]int64{math.MinInt64, 0, math.MaxInt64},
	}

	checkEncoding(t, value)
	checkTypedEncoding(t, value)
	checkEncoding(t, nestedScalarLimits{Before: true, Value: value, After: false})
	checkEncoding(t, struct{}{})
	checkScalarCollection(t, []scalarLimits{value, {}})

	large := reflect.StructOf([]reflect.StructField{{Name: "Values", Type: reflect.TypeFor[[256]scalarLimits]()}})
	checkEncoding(t, reflect.New(large).Interface())
}

func TestBackendWriterErrors(t *testing.T) {
	values := []any{make([]int64, 129), make([]uint64, 129), make([]float64, 129), make([]bool, 129), make([]scalarLimits, 129)}

	for _, value := range values {
		writer := &backendWriter{err: io.ErrClosedPipe}
		encoder := NewEncoder(writer)
		encoder.flushThreshold = 1

		err := encoder.Encode(value)
		if !errors.Is(err, io.ErrClosedPipe) || writer.writes != 1 {
			t.Fatalf("%T: writes=%d error=%v", value, writer.writes, err)
		}
	}
}

func TestStringEscapeClusters(t *testing.T) {
	for gap := range 17 {
		var input bytes.Buffer

		for index := range 256 {
			input.WriteString("\"\xff\u2028\u20ac\U0001f600")
			input.Write(bytes.Repeat([]byte{'a'}, gap))
			input.WriteByte(byte(index))
		}

		checkString(t, input.String())
	}
}

func checkScalarCollection[Value any](t *testing.T, seeds []Value) {
	t.Helper()

	lengths := []int{0, 1, 63, 64, 65, 127, 128, 129, 1024}
	checkEncoding(t, []Value(nil))

	for _, length := range lengths {
		values := make([]Value, length)

		for index := range values {
			values[index] = seeds[index%len(seeds)]
		}

		checkEncoding(t, values)
		checkTypedEncoding(t, values)

		array := reflect.New(reflect.ArrayOf(length, reflect.TypeFor[Value]())).Elem()
		reflect.Copy(array, reflect.ValueOf(values))
		checkEncoding(t, array.Interface())

		// Box once and warm the reusable buffer before measuring the encoder.
		var boxed any = values

		encoder := NewEncoder(io.Discard)

		err := encoder.Encode(boxed)
		if err != nil {
			t.Fatal(err)
		}

		allocations := testing.AllocsPerRun(10, func() {
			encodeError := encoder.Encode(boxed)
			if encodeError != nil {
				t.Fatal(encodeError)
			}
		})

		if allocations != 0 {
			t.Fatalf("%T length %d: got %v allocations", values, length, allocations)
		}
	}
}
