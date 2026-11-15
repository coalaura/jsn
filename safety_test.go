package jsn

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"reflect"
	"runtime"
	"runtime/debug"
	"strings"
	"testing"
	"time"
)

type callbackJSON int

type callbackText int

type callbackWriter int

type callbackDirect struct {
	Value *int
}

type callbackArray [1]*int

type callbackBytes []byte

type callbackJSONDirect struct {
	Value *int
}

type callbackTextDirect struct {
	Value *int
}

type callbackFields struct {
	JSON callbackJSON
	Text callbackText
}

type cycleNode struct {
	Next *cycleNode
	Any  any
}

type outputJSON string

type outputError struct{}

type jsonByte byte

type textByte byte

var (
	retainedWriters  []*callbackWriter
	retainedDirect   []*callbackDirect
	retainedArrays   []*callbackArray
	retainedJSON     []*callbackJSONDirect
	retainedText     []*callbackTextDirect
	errOutputFailure = errors.New("custom output failure")
)

func (value *callbackJSON) MarshalJSON() ([]byte, error) {
	return []byte(`"json"`), nil
}

func (value *callbackText) MarshalText() ([]byte, error) {
	return []byte("text"), nil
}

func (value *callbackWriter) MarshalJSONTo(writer io.Writer) error {
	retainedWriters = append(retainedWriters, value)
	*value = 99

	_, err := io.WriteString(writer, "99")
	return err
}

func (value *callbackDirect) MarshalJSONTo(writer io.Writer) error {
	retainedDirect = append(retainedDirect, value)
	value.Value = new(99)

	_, err := io.WriteString(writer, "99")
	return err
}

func (value *callbackArray) MarshalJSONTo(writer io.Writer) error {
	retainedArrays = append(retainedArrays, value)
	value[0] = new(99)

	_, err := io.WriteString(writer, "99")
	return err
}

func (value *callbackBytes) MarshalJSON() ([]byte, error) {
	return []byte(`"bytes"`), nil
}

func (value *callbackJSONDirect) MarshalJSON() ([]byte, error) {
	retainedJSON = append(retainedJSON, value)
	value.Value = new(99)

	return []byte("99"), nil
}

func (value *callbackTextDirect) MarshalText() ([]byte, error) {
	retainedText = append(retainedText, value)
	value.Value = new(99)

	return []byte("99"), nil
}

func (value outputJSON) MarshalJSON() ([]byte, error) {
	return []byte(value), nil
}

func (value outputError) MarshalJSON() ([]byte, error) {
	return nil, errOutputFailure
}

func (value *jsonByte) UnmarshalJSON(data []byte) error {
	*value = 42

	return nil
}

func (value *textByte) UnmarshalText(data []byte) error {
	*value = 43

	return nil
}

func TestCallbackAddressability(t *testing.T) {
	values := []any{
		callbackJSON(7), callbackText(7), callbackFields{JSON: 7, Text: 7},
		&callbackFields{JSON: 7, Text: 7}, []callbackJSON{7}, [1]callbackJSON{7},
		new([1]callbackJSON{7}), map[string]callbackJSON{"value": 7},
		map[string]callbackFields{"value": {JSON: 7, Text: 7}},
		map[string]*callbackJSON{"value": new(callbackJSON(7))},
		callbackBytes{1, 2}, new(callbackBytes{1, 2}), map[int]callbackBytes{1: {1, 2}},
		callbackJSONDirect{Value: new(7)}, callbackTextDirect{Value: new(7)},
	}

	for _, value := range values {
		want, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}

		var output bytes.Buffer

		err = NewEncoder(&output).Encode(value)
		if err != nil || strings.TrimSpace(output.String()) != string(want) {
			t.Errorf("%T: got %s, %v; want %s", value, output.Bytes(), err, want)
		}
	}

	value := callbackFields{JSON: 7, Text: 7}

	want, err := json.Marshal(&value)
	if err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer

	err = CompileTyped[callbackFields]().Encode(NewEncoder(&output), &value)
	if err != nil || strings.TrimSpace(output.String()) != string(want) {
		t.Fatalf("typed: got %s, %v; want %s", output.Bytes(), err, want)
	}
}

func TestCallbackStorage(t *testing.T) {
	if os.Getenv("JSN_STORAGE_CHILD") != "1" {
		runSafetyChild(t, "JSN_STORAGE_CHILD", "1", "TestCallbackStorage")

		return
	}

	encoder := NewEncoder(io.Discard)
	boxed := any(callbackWriter(7))
	direct := callbackDirect{Value: new(7)}
	array := callbackArray{new(7)}
	values := []any{boxed, direct, array, map[int]callbackWriter{1: 7, 2: 8}}

	for range 2 {
		for _, value := range values {
			err := encoder.Encode(value)
			if err != nil {
				t.Fatal(err)
			}
		}
	}

	runtime.GC()

	if boxed.(callbackWriter) != 7 || *direct.Value != 7 || *array[0] != 7 {
		t.Fatal("callback mutated unaddressable input storage")
	}

	seen := make(map[*callbackWriter]bool, len(retainedWriters))

	for _, receiver := range retainedWriters {
		if seen[receiver] || *receiver != 99 {
			t.Fatal("writer receiver was reused or changed")
		}

		seen[receiver] = true
	}

	if len(retainedDirect) != 2 || retainedDirect[0] == retainedDirect[1] || retainedDirect[0].Value == nil || *retainedDirect[0].Value != 99 {
		t.Fatal("direct struct receiver aliases encoder scratch")
	}

	if len(retainedArrays) != 2 || retainedArrays[0] == retainedArrays[1] || retainedArrays[0][0] == nil || *retainedArrays[0][0] != 99 {
		t.Fatal("direct array receiver aliases encoder scratch")
	}

	typed := callbackWriter(7)
	err := CompileTyped[callbackWriter]().Encode(encoder, &typed)

	if err != nil || typed != 99 || retainedWriters[len(retainedWriters)-1] != &typed {
		t.Fatalf("typed receiver lost addressability: %v", err)
	}
}

func TestEncodingCycles(t *testing.T) {
	kind := os.Getenv("JSN_CYCLE_CHILD")
	if kind == "" {
		kinds := []string{"pointer", "map", "slice", "indirect", "typed"}

		for _, kind := range kinds {
			t.Run(kind, func(t *testing.T) {
				runSafetyChild(t, "JSN_CYCLE_CHILD", kind, "TestEncodingCycles")
			})
		}

		return
	}

	debug.SetMaxStack(256 << 10)
	encoder := NewEncoder(io.Discard)
	node := new(cycleNode)
	node.Next = node

	var value any = node

	switch kind {
	case "map":
		mapping := make(map[string]any)
		mapping["self"] = mapping
		value = mapping
	case "slice":
		slice := make([]any, 1)
		slice[0] = slice
		value = slice
	case "indirect":
		node.Next = nil
		node.Any = map[int]any{1: []any{node}}
	}

	var err error

	if kind == "typed" {
		err = CompileTyped[cycleNode]().Encode(encoder, node)
	} else {
		err = encoder.Encode(value)
	}

	cycle, ok := errors.AsType[*json.UnsupportedValueError](err)
	if !ok || !cycle.Value.IsValid() || cycle.Value.IsNil() {
		t.Fatalf("cycle error: %v", err)
	}

	node.Next = nil
	node.Any = nil

	err = encoder.Encode([]*cycleNode{node, node})
	if err != nil {
		t.Fatalf("shared acyclic reference after error: %v", err)
	}

	slice := make([]any, 2)
	slice[0] = slice[:0]
	slice[1] = map[string]any{"node": node}

	err = encoder.Encode([]any{slice, slice})
	if err != nil {
		t.Fatalf("shared slices and maps: %v", err)
	}
}

func TestStandardCallbackStorage(t *testing.T) {
	retainedJSON = nil
	retainedText = nil

	encoder := NewEncoder(io.Discard)
	jsonValue := callbackJSONDirect{Value: new(7)}
	textValue := callbackTextDirect{Value: new(7)}
	values := []any{&jsonValue, &textValue, map[int]*callbackJSONDirect{1: &jsonValue}, map[int]*callbackTextDirect{1: &textValue}}

	for _, value := range values {
		err := encoder.Encode(value)
		if err != nil {
			t.Fatal(err)
		}
	}

	err := CompileTyped[callbackJSONDirect]().Encode(encoder, &jsonValue)
	if err != nil {
		t.Fatal(err)
	}

	err = CompileTyped[callbackTextDirect]().Encode(encoder, &textValue)
	if err != nil {
		t.Fatal(err)
	}

	runtime.GC()

	if len(retainedJSON) != 3 || len(retainedText) != 3 {
		t.Fatal("missing pointer callbacks")
	}

	for index := range retainedJSON {
		if retainedJSON[index] != &jsonValue || retainedText[index] != &textValue || *retainedJSON[index].Value != 99 || *retainedText[index].Value != 99 {
			t.Fatal("standard marshaler receiver lost its identity or mutation")
		}
	}
}

func TestCustomJSONValidation(t *testing.T) {
	inputs := []string{"", " ", "{", "1 2", "null trailing", `{"x":1,}`, " { \"html\" : \"<>&\u2028\u2029\" } ", "null", "42", `"text"`}

	for _, input := range inputs {
		values := []any{json.RawMessage(input), outputJSON(input)}

		for _, value := range values {
			want, wantError := json.Marshal(value)

			var output bytes.Buffer

			err := NewEncoder(&output).Encode(value)
			if (err == nil) != (wantError == nil) {
				t.Errorf("%T %q: error %v, want %v", value, input, err, wantError)

				continue
			}

			if err == nil && output.String() != string(want)+"\n" {
				t.Errorf("%T: got %q, want %q", value, output.String(), want)
			}

			if err != nil {
				_, ok := errors.AsType[*json.MarshalerError](err)
				if !ok || errors.Unwrap(err) == nil {
					t.Errorf("missing marshaler error wrapping: %v", err)
				}
			}
		}
	}

	err := NewEncoder(io.Discard).Encode(outputError{})
	_, wrapped := errors.AsType[*json.MarshalerError](err)

	if !wrapped || !errors.Is(err, errOutputFailure) {
		t.Fatalf("callback error wrapping: %v", err)
	}
}

func TestNamedByteDecoding(t *testing.T) {
	inputs := []string{`"AQI="`, `""`, `"AQ\u0049="`, `"bad"`, `null`, `[1,2]`, `["a","b"]`, `[]`}

	for _, input := range inputs {
		t.Run(input, func(t *testing.T) {
			checkDecode[[]jsonByte](t, []byte(input))
			checkDecode[[]textByte](t, []byte(input))
		})
	}
}

func runSafetyChild(t *testing.T, key, value, test string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^"+test+"$")
	command.Env = append(os.Environ(), key+"="+value)

	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("subprocess: %v\n%s", err, output[:min(len(output), 2500)])
	}
}

func TestSharedMapReferences(t *testing.T) {
	shared := map[int]int{1: 2}
	value := []any{shared, shared}

	var output bytes.Buffer

	err := NewEncoder(&output).Encode(value)
	if err != nil {
		t.Fatal(err)
	}

	var got any

	err = json.Unmarshal(output.Bytes(), &got)
	if err != nil || !reflect.DeepEqual(got, []any{map[string]any{"1": float64(2)}, map[string]any{"1": float64(2)}}) {
		t.Fatalf("shared map: %s, %v", output.Bytes(), err)
	}
}

func FuzzCustomJSON(f *testing.F) {
	inputs := []string{``, `null`, `42`, ` true `, `[1, 2, {"value":"<>&"}]`, `"a \\"`, `"\u2028\u2029"`, `{} []`, `"\x20"`, "\"\xff\""}

	for _, input := range inputs {
		f.Add([]byte(input))
	}

	f.Fuzz(func(t *testing.T, raw []byte) {
		if len(raw) > 4096 {
			return
		}

		values := []any{json.RawMessage(raw), outputJSON(raw)}

		for _, value := range values {
			want, wantErr := json.Marshal(value)

			var output bytes.Buffer

			err := NewEncoder(&output).Encode(value)
			if (err == nil) != (wantErr == nil) {
				t.Fatalf("%T %q: got %v, want %v", value, raw, err, wantErr)
			}

			if err == nil && !bytes.Equal(bytes.TrimSuffix(output.Bytes(), []byte{'\n'}), want) {
				t.Fatalf("%T %q: got %s, want %s", value, raw, output.Bytes(), want)
			}
		}
	})
}
