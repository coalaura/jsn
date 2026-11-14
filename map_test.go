package jsn

import (
	"bytes"
	"encoding"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strconv"
	"sync"
	"testing"
)

type genericMapRecord struct {
	Name  string
	Count int
}

type recursiveGenericMap map[int]recursiveGenericMap

type genericMapNode struct {
	Children map[int]genericMapNode
	Tail     string
}

type genericMapCase struct {
	name  string
	value any
}

type retainingMapValue struct {
	Value int
	Saved *[]*retainingMapValue
}

type retainingMapHolder struct {
	Value retainingMapValue
}

type failingMapValue int

type failingMapWriter struct{}

type staticMapText struct {
	Value int
}

type staticMapJSON struct {
	Value int
}

type retainingMapZero struct {
	Value int
	Saved *[]*retainingMapZero
}

type retainingMapZeroHolder struct {
	Value retainingMapZero `json:"value,omitzero"`
}

var (
	errMapValue        = errors.New("map value failure")
	staticMapTextBytes = []byte("key")
	staticMapJSONBytes = []byte("42")
)

func (value retainingMapValue) MarshalJSON() ([]byte, error) {
	return staticMapJSONBytes, nil
}

func (value *retainingMapValue) MarshalJSONTo(writer io.Writer) error {
	*value.Saved = append(*value.Saved, value)
	_, err := io.WriteString(writer, strconv.Itoa(value.Value))

	return err
}

func (value failingMapValue) MarshalJSONTo(writer io.Writer) error {
	if value == 2 {
		panic(errMapValue)
	}

	return errMapValue
}

func (writer failingMapWriter) Write(data []byte) (int, error) {
	return 0, errMapValue
}

func (value staticMapText) MarshalText() ([]byte, error) {
	if value.Value < 0 {
		return nil, errMapValue
	}

	return staticMapTextBytes, nil
}

func (value staticMapJSON) MarshalJSON() ([]byte, error) {
	return staticMapJSONBytes, nil
}

func (value *retainingMapZero) IsZero() bool {
	*value.Saved = append(*value.Saved, value)

	return true
}

func TestGenericMaps(t *testing.T) {
	for _, test := range genericMapCases() {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer

			encoder := NewEncoder(&output)

			err := encoder.Encode(test.value)
			if err != nil {
				t.Fatal(err)
			}

			want, err := json.Marshal(test.value)
			if err != nil {
				t.Fatal(err)
			}

			var (
				gotValue  any
				wantValue any
			)

			err = json.Unmarshal(output.Bytes(), &gotValue)
			if err != nil {
				t.Fatalf("invalid JSON %q: %v", output.Bytes(), err)
			}

			err = json.Unmarshal(want, &wantValue)
			if err != nil {
				t.Fatal(err)
			}

			if !reflect.DeepEqual(gotValue, wantValue) {
				t.Fatalf("got %s, want %s", output.Bytes(), want)
			}
		})
	}
}

func TestGenericMapsDoNotAllocate(t *testing.T) {
	for _, test := range genericMapCases() {
		t.Run(test.name, func(t *testing.T) {
			encoder := NewEncoder(io.Discard)

			err := encoder.Encode(test.value)
			if err != nil {
				t.Fatal(err)
			}

			allocations := testing.AllocsPerRun(100, func() {
				err = encoder.Encode(test.value)
				if err != nil {
					t.Fatal(err)
				}
			})

			if allocations != 0 {
				t.Fatalf("got %g allocations per encoding", allocations)
			}
		})
	}
}

func TestGenericMapRetainedReceivers(t *testing.T) {
	var saved []*retainingMapValue

	value := map[string]retainingMapHolder{
		"first":  {Value: retainingMapValue{Value: 1, Saved: &saved}},
		"second": {Value: retainingMapValue{Value: 2, Saved: &saved}},
	}

	encoder := NewEncoder(io.Discard)

	for range 2 {
		err := encoder.Encode(value)
		if err != nil {
			t.Fatal(err)
		}
	}

	if len(saved) != 4 {
		t.Fatalf("got %d receivers, want 4", len(saved))
	}

	seen := make(map[*retainingMapValue]bool, len(saved))
	total := 0

	for _, receiver := range saved {
		if seen[receiver] {
			t.Fatal("marshaler receiver reused between entries")
		}

		seen[receiver] = true
		total += receiver.Value
	}

	if total != 6 {
		t.Fatalf("retained receivers changed: sum %d, want 6", total)
	}
}

func TestGenericMapRetainedZeroReceivers(t *testing.T) {
	var saved []*retainingMapZero

	value := map[string]retainingMapZeroHolder{
		"first":  {Value: retainingMapZero{Value: 1, Saved: &saved}},
		"second": {Value: retainingMapZero{Value: 2, Saved: &saved}},
	}

	encoder := NewEncoder(io.Discard)

	err := encoder.Encode(value)
	if err != nil {
		t.Fatal(err)
	}

	if len(saved) != 2 || saved[0] == saved[1] || saved[0].Value+saved[1].Value != 3 {
		t.Fatal("IsZero receiver copies were reused or cleared")
	}
}

func TestGenericMapScratchCleanup(t *testing.T) {
	encoder := NewEncoder(io.Discard)
	values := []any{
		map[string]*genericMapRecord{"key": {Name: "retained", Count: 1}},
		map[int][]string{1: {"retained"}},
		recursiveGenericMap{1: {2: {3: {}}}},
		map[staticMapText]int{{Value: 1}: 1},
	}

	for _, value := range values {
		err := encoder.Encode(value)
		if err != nil {
			t.Fatal(err)
		}

		checkMapScratchCleared(t, encoder)
	}

	err := encoder.Encode(map[string]failingMapValue{"key": 1})
	if !errors.Is(err, errMapValue) {
		t.Fatalf("got %v, want map value failure", err)
	}

	checkMapScratchCleared(t, encoder)

	err = encoder.Encode(map[staticMapText]int{{Value: -1}: 1})
	if !errors.Is(err, errMapValue) {
		t.Fatalf("got %v, want map key failure", err)
	}

	checkMapScratchCleared(t, encoder)

	checkMapPanic(t, encoder)
	checkMapScratchCleared(t, encoder)

	encoder.wr = failingMapWriter{}
	encoder.flushThreshold = 1

	err = encoder.Encode(values[0])
	if !errors.Is(err, errMapValue) {
		t.Fatalf("got %v, want writer failure", err)
	}

	checkMapScratchCleared(t, encoder)

	encoder.wr = io.Discard

	for _, value := range values {
		err = encoder.Encode(value)
		if err != nil {
			t.Fatal(err)
		}
	}

	checkMapScratchCleared(t, encoder)
}

func TestGenericMapSharedTypedEncoder(t *testing.T) {
	compiled := CompileTyped[map[int]genericMapRecord]()
	value := map[int]genericMapRecord{1: {Name: "one", Count: 1}}

	want, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}

	want = append(want, '\n')

	var workers sync.WaitGroup

	for range 8 {
		workers.Go(func() {
			var output bytes.Buffer

			encoder := NewEncoder(&output)

			for range 50 {
				output.Reset()

				err := compiled.Encode(encoder, &value)
				if err != nil {
					t.Error(err)

					return
				}

				if !bytes.Equal(output.Bytes(), want) {
					t.Errorf("got %s, want %s", output.Bytes(), want)

					return
				}
			}
		})
	}

	workers.Wait()
}

func BenchmarkGenericMaps(b *testing.B) {
	for _, test := range genericMapCases() {
		b.Run(test.name, func(b *testing.B) {
			encoder := NewEncoder(io.Discard)

			err := encoder.Encode(test.value)
			if err != nil {
				b.Fatal(err)
			}

			b.ReportAllocs()

			for b.Loop() {
				err = encoder.Encode(test.value)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func genericMapCases() []genericMapCase {
	large := make(map[int]genericMapRecord, 256)

	for index := range 256 {
		large[index] = genericMapRecord{Name: "entry", Count: index}
	}

	return []genericMapCase{
		{name: "string_int32", value: map[string]int32{"a": 1, "b": 2, "c": 3}},
		{name: "string_struct", value: map[string]genericMapRecord{"a": {Name: "one", Count: 1}, "b": {Name: "two", Count: 2}, "c": {Name: "three", Count: 3}}},
		{name: "int_int", value: map[int]int{1: 1, 2: 2, 3: 3}},
		{name: "uint_string", value: map[uint64]string{0: "zero", 18446744073709551615: "max"}},
		{name: "string_array", value: map[string][2]int{"a": {1, 2}, "b": {3, 4}}},
		{name: "string_slice", value: map[string][]int{"a": {1, 2}, "b": {3, 4}}},
		{name: "string_pointer", value: map[string]*genericMapRecord{"a": {Name: "one", Count: 1}, "b": nil}},
		{name: "direct_wrapper", value: map[string]directWrapper{"a": {Value: new(42)}, "b": {}}},
		{name: "int_interface", value: map[int]any{1: genericMapRecord{Name: "one", Count: 1}, 2: "two", 3: nil}},
		{name: "method_interface", value: map[string]valueMethod{"a": methodValue{Value: 1}, "b": nil}},
		{name: "nested", value: map[int]map[string]int32{1: {"a": 1}, 2: {"b": 2}}},
		{name: "recursive", value: recursiveGenericMap{1: {2: {}}, 3: {4: nil}}},
		{name: "recursive_struct", value: map[int]genericMapNode{1: {Children: map[int]genericMapNode{2: {Children: map[int]genericMapNode{3: {Tail: "leaf"}}, Tail: "child"}}, Tail: "root"}}},
		{name: "text_key", value: map[staticMapText]int{{Value: 1}: 1}},
		{name: "text_pointer_key", value: map[*staticMapText]int{new(staticMapText{Value: 1}): 1, nil: 2}},
		{name: "text_interface_key", value: map[encoding.TextMarshaler]int{staticMapText{Value: 1}: 1}},
		{name: "json_value", value: map[string]staticMapJSON{"a": {Value: 1}, "b": {Value: 2}}},
		{name: "number_value", value: map[string]json.Number{"a": "1.5", "b": "2e3"}},
		{name: "large", value: large},
		{name: "nil", value: map[int]genericMapRecord(nil)},
		{name: "empty", value: map[int]genericMapRecord{}},
	}
}

func checkMapPanic(t *testing.T, encoder *Encoder) {
	t.Helper()

	defer func() {
		failure, ok := recover().(error)
		if !ok || !errors.Is(failure, errMapValue) {
			t.Fatalf("got panic %v, want map value failure", failure)
		}
	}()

	_ = encoder.Encode(map[string]failingMapValue{"key": 2})
}

func checkMapScratchCleared(t *testing.T, encoder *Encoder) {
	t.Helper()

	for mapType, pool := range encoder.mapScratch {
		if pool.free == nil {
			t.Fatalf("scratch for %v was not returned", mapType)
		}

		for scratch := pool.free; scratch != nil; scratch = scratch.next {
			if !scratch.key.IsZero() || scratch.value.IsValid() && !scratch.value.IsZero() {
				t.Fatalf("scratch for %v retains an entry", mapType)
			}
		}
	}
}
