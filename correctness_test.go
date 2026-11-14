package jsn

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

type correctnessCase struct {
	name  string
	value any
}

type directWrapper struct {
	Value *int
}

type directMapWrapper struct {
	Value map[string]int
}

type directNestedWrapper struct {
	Value [1]directWrapper
}

type namedBytes []byte

type namedByte byte

type marshaledInt int

type marshaledArray [2]int

type marshaledMap map[string]int

type directMarshaler struct {
	Value *int
}

type directWriterMarshaler struct {
	Value *int
}

type namedText int

type methodValue struct {
	Value int
}

type valueMethod interface {
	GetValue() int
}

type methodHolder struct {
	Value valueMethod
}

type marshalerHolder struct {
	Value json.Marshaler
}

type writerMarshalerHolder struct {
	Value WriterMarshaler
}

type namedArrayHolder struct {
	Value marshaledArray
}

type quotedFields struct {
	String string      `json:"string,string"`
	Int    int64       `json:"int,string"`
	Uint   uint64      `json:"uint,string"`
	Float  float64     `json:"float,string"`
	Bool   bool        `json:"bool,string"`
	Ptr    *int        `json:"ptr,string"`
	Nil    *string     `json:"nil,string"`
	Number json.Number `json:"number,string"`
}

type omittedArrays struct {
	Empty [0]int `json:"empty,omitempty"`
	Value [2]int `json:"value,omitempty"`
}

type EmbeddedLeft struct {
	Value int
	Left  string
}

type EmbeddedRight struct {
	Value int
	Right string
}

type embeddedConflict struct {
	EmbeddedLeft
	EmbeddedRight
}

type embeddedDominance struct {
	EmbeddedLeft
	Value int
}

type embeddedPointer struct {
	*EmbeddedLeft
	Keep int
}

type embeddedRecursive struct {
	*embeddedRecursive
	Value int
}

type escapedNames struct {
	Value int `json:"<value>&"`
}

type escapedOmittedNames struct {
	Value int `json:"<value>&,omitempty"`
}

type zeroMember struct {
	Value int
}

type nestedZero struct {
	Member zeroMember
}

type omittedNestedZero struct {
	Value nestedZero `json:"value,omitzero"`
}

type zeroInterface interface {
	IsZero() bool
}

type omittedZeroInterface struct {
	Value zeroInterface `json:"value,omitzero"`
}

type directZero struct {
	Value *int
}

type omittedDirectZero struct {
	Value directZero `json:"value,omitzero"`
}

type timeFields struct {
	Value time.Time
	Ptr   *time.Time
	List  []time.Time
}

type omittedTimeFields struct {
	Value time.Time `json:"value,omitzero"`
}

func (value marshaledInt) MarshalJSON() ([]byte, error) {
	return []byte(`"custom"`), nil
}

func (value marshaledArray) MarshalJSON() ([]byte, error) {
	return []byte(`"array"`), nil
}

func (value marshaledMap) MarshalJSON() ([]byte, error) {
	return []byte(`"map"`), nil
}

func (value directMarshaler) MarshalJSON() ([]byte, error) {
	if value.Value == nil {
		return []byte(`"nil-value"`), nil
	}

	return strconv.AppendInt(nil, int64(*value.Value), 10), nil
}

func (value directWriterMarshaler) MarshalJSONTo(writer io.Writer) error {
	_, err := writer.Write([]byte(`"writer"`))

	return err
}

func (value namedText) MarshalText() ([]byte, error) {
	return []byte("text"), nil
}

func (value methodValue) GetValue() int {
	return value.Value
}

func (value zeroMember) IsZero() bool {
	return value.Value == 1
}

func (value directZero) IsZero() bool {
	return value.Value != nil && *value.Value == 0
}

func TestCorrectnessRegressions(t *testing.T) {
	stamp := time.Date(2026, 1, 2, 3, 4, 5, 123456789, time.FixedZone("offset", 3600))

	cases := []correctnessCase{
		{name: "direct struct", value: directWrapper{Value: new(42)}},
		{name: "nil direct struct", value: directWrapper{}},
		{name: "direct array", value: [1]*int{new(42)}},
		{name: "nil direct array", value: [1]*int{}},
		{name: "nested direct value", value: directNestedWrapper{Value: [1]directWrapper{{Value: new(42)}}}},
		{name: "direct map wrapper", value: directMapWrapper{Value: map[string]int{"value": 42}}},
		{name: "direct interface collection", value: []any{directWrapper{Value: new(42)}, [1]*int{new(7)}, directMapWrapper{Value: map[string]int{"value": 3}}}},
		{name: "direct value marshaler", value: directMarshaler{Value: new(42)}},
		{name: "nil-word value marshaler", value: directMarshaler{}},
		{name: "nil map marshaler", value: marshaledMap(nil)},
		{name: "map named marshaler", value: map[string]marshaledInt{"key": 7}},
		{name: "slice named marshaler", value: []marshaledInt{7}},
		{name: "array marshaler in tape", value: namedArrayHolder{Value: marshaledArray{1, 2}}},
		{name: "method interface", value: methodHolder{Value: methodValue{Value: 42}}},
		{name: "method interface pointer", value: methodHolder{Value: &methodValue{Value: 42}}},
		{name: "nil method interface", value: methodHolder{}},
		{name: "typed nil method interface", value: methodHolder{Value: (*methodValue)(nil)}},
		{name: "method interface map", value: map[string]valueMethod{"key": methodValue{Value: 42}}},
		{name: "method interface slice", value: []valueMethod{methodValue{Value: 42}, (*methodValue)(nil), nil}},
		{name: "marshaler interface", value: marshalerHolder{Value: marshaledInt(7)}},
		{name: "named bytes", value: namedBytes{1, 2, 3}},
		{name: "nil named bytes", value: namedBytes(nil)},
		{name: "empty named bytes", value: namedBytes{}},
		{name: "named byte elements", value: []namedByte{1, 2, 3}},
		{name: "number", value: json.Number("-1.25e+1000")},
		{name: "empty number", value: json.Number("")},
		{name: "number map", value: map[string]json.Number{"key": "123"}},
		{name: "number slice", value: []json.Number{"123", ""}},
		{name: "uintptr", value: uintptr(42)},
		{name: "integer map keys ignore JSON marshaler", value: map[marshaledInt]int{7: 1}},
		{name: "text map keys", value: map[namedText]int{7: 1}},
		{name: "text map values", value: map[string]namedText{"key": 7}},
		{name: "quoted fields", value: quotedFields{String: "<quote>\"\n\t\\\u20ac", Int: -42, Uint: 42, Float: 1.25, Bool: true, Ptr: new(7), Number: "123"}},
		{name: "omitted arrays", value: omittedArrays{}},
		{name: "embedded conflict", value: embeddedConflict{EmbeddedLeft: EmbeddedLeft{Value: 1, Left: "left"}, EmbeddedRight: EmbeddedRight{Value: 2, Right: "right"}}},
		{name: "embedded dominance", value: embeddedDominance{EmbeddedLeft: EmbeddedLeft{Value: 1, Left: "left"}, Value: 2}},
		{name: "embedded pointer", value: embeddedPointer{EmbeddedLeft: &EmbeddedLeft{Value: 42, Left: "left"}, Keep: 7}},
		{name: "nil embedded pointer", value: embeddedPointer{Keep: 7}},
		{name: "recursive embedded pointer", value: embeddedRecursive{embeddedRecursive: nil, Value: 42}},
		{name: "escaped field name in tape", value: escapedNames{Value: 42}},
		{name: "escaped field name with omission", value: escapedOmittedNames{Value: 42}},
		{name: "nested zero ignores member method", value: omittedNestedZero{Value: nestedZero{Member: zeroMember{Value: 1}}}},
		{name: "nested zero uses actual value", value: omittedNestedZero{}},
		{name: "nil zero interface", value: omittedZeroInterface{}},
		{name: "typed nil zero interface", value: omittedZeroInterface{Value: (*zeroMember)(nil)}},
		{name: "nonzero interface", value: omittedZeroInterface{Value: zeroMember{Value: 2}}},
		{name: "zero interface", value: omittedZeroInterface{Value: zeroMember{Value: 1}}},
		{name: "nil-word zero receiver", value: omittedDirectZero{}},
		{name: "direct zero receiver", value: omittedDirectZero{Value: directZero{Value: new(0)}}},
		{name: "timestamp", value: stamp},
		{name: "timestamp tape and slice", value: timeFields{Value: stamp, Ptr: &stamp, List: []time.Time{stamp}}},
		{name: "timestamp omission path", value: omittedTimeFields{Value: stamp}},
	}

	for _, current := range cases {
		t.Run(current.name, func(t *testing.T) {
			checkEncoding(t, current.value)
		})
	}

	checkTypedEncoding(t, directWrapper{Value: new(42)})
	checkTypedEncoding(t, [1]*int{new(42)})
	checkTypedEncoding(t, methodHolder{Value: methodValue{Value: 42}})
	checkTypedEncoding[valueMethod](t, methodValue{Value: 42})
	checkTypedEncoding[json.Marshaler](t, marshaledInt(7))
	checkTypedEncoding(t, stamp)
	checkTypedEncoding(t, quotedFields{String: strings.Repeat("\"\\<\u20ac", 1000)})
}

func TestWriterMarshalerInterfaces(t *testing.T) {
	cases := []correctnessCase{
		{name: "direct", value: directWriterMarshaler{}},
		{name: "interface", value: writerMarshalerHolder{Value: directWriterMarshaler{}}},
		{name: "map", value: map[string]WriterMarshaler{"Value": directWriterMarshaler{}}},
	}

	for _, current := range cases {
		t.Run(current.name, func(t *testing.T) {
			var output bytes.Buffer

			err := NewEncoder(&output).Encode(current.value)
			if err != nil {
				t.Fatal(err)
			}

			want := "{\"Value\":\"writer\"}\n"

			if current.name == "direct" {
				want = "\"writer\"\n"
			}

			if output.String() != want {
				t.Fatalf("got %q, want %q", output.String(), want)
			}
		})
	}
}

func TestNilMarshalerInterface(t *testing.T) {
	var output bytes.Buffer

	err := NewEncoder(&output).Encode(marshalerHolder{Value: (*marshaledInt)(nil)})
	if err != nil {
		t.Fatal(err)
	}

	if output.String() != "{\"Value\":null}\n" {
		t.Fatalf("got %q, want a null interface field", output.String())
	}
}

func TestRejectedValues(t *testing.T) {
	values := []any{
		complex(1, 2),
		make(chan int),
		func() {},
		map[bool]int{},
		map[float64]int(nil),
		json.Number("01"),
		json.Number("-"),
		json.Number("1."),
		json.Number(".1"),
		json.Number("1e"),
		json.Number("1e+"),
		json.Number("NaN"),
		json.Number("true"),
		json.Number("1 2"),
		time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.FixedZone("invalid", 24*3600)),
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.FixedZone("invalid", -24*3600)),
	}

	for _, value := range values {
		err := NewEncoder(io.Discard).Encode(value)
		if err == nil {
			t.Fatalf("accepted invalid value of type %T: %v", value, value)
		}
	}
}

func TestStringValidity(t *testing.T) {
	suffixes := []string{"\xff", "\xc0\x80", "\xed\xa0\x80", "\xf4\x90\x80\x80", "\xe2", "\xe2\x80", "\u20ac", "\u2028", "\u2029", "\ufffd", "\U0001f600", "\b\f\n\r\t\\\"<&>"}
	lengths := []int{0, 1, 7, 8, 15, 16, 31, 32, 47, 48, 63, 64, 127, 128, 129, 255, 4096}

	for _, length := range lengths {
		prefix := strings.Repeat("a", length)

		for _, suffix := range suffixes {
			checkString(t, prefix+suffix)
			checkString(t, "\""+prefix+suffix+strings.Repeat("z", 160))
		}

		for value := range 256 {
			checkString(t, prefix+string([]byte{byte(value)})+"z")
		}
	}
}

func TestCorrectedPathsDoNotAllocate(t *testing.T) {
	stamp := time.Date(2026, 1, 2, 3, 4, 5, 123456789, time.FixedZone("offset", 3600))

	values := []any{
		directWrapper{Value: new(42)},
		directNestedWrapper{Value: [1]directWrapper{{Value: new(42)}}},
		methodHolder{Value: methodValue{Value: 42}},
		[]valueMethod{methodValue{Value: 42}},
		stamp,
		quotedFields{String: "<quote>\"\n\\\u20ac", Number: "123"},
		namedBytes{1, 2, 3},
		strings.Repeat("\u20ac\xff", 100),
	}

	for _, value := range values {
		encoder := NewEncoder(io.Discard)

		err := encoder.Encode(value)
		if err != nil {
			t.Fatal(err)
		}

		allocations := testing.AllocsPerRun(100, func() {
			encodeError := encoder.Encode(value)
			if encodeError != nil {
				t.Fatal(encodeError)
			}
		})

		if allocations != 0 {
			t.Errorf("%T: got %v allocations per encode, want zero", value, allocations)
		}
	}
}

func FuzzStringEncoding(f *testing.F) {
	seeds := []string{"", "ASCII", "\xff", "\xe2\x80", "\u20ac\u2028\U0001f600", strings.Repeat("a", 129) + "\xff", "\"\b\f\n\\"}

	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, value string) {
		checkString(t, value)
	})
}

func checkEncoding(t *testing.T, value any) {
	t.Helper()

	want, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer

	err = NewEncoder(&output).Encode(value)
	if err != nil {
		t.Fatal(err)
	}

	got := bytes.TrimSuffix(output.Bytes(), []byte{'\n'})
	if !bytes.Equal(got, want) {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func checkTypedEncoding[T any](t *testing.T, value T) {
	t.Helper()

	want, err := json.Marshal(&value)
	if err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer

	compiled := CompileTyped[T]()

	err = compiled.Encode(NewEncoder(&output), &value)
	if err != nil {
		t.Fatal(err)
	}

	got := bytes.TrimSuffix(output.Bytes(), []byte{'\n'})
	if !bytes.Equal(got, want) {
		t.Fatalf("typed %T: got %s, want %s", value, got, want)
	}
}

func checkString(t *testing.T, value string) {
	t.Helper()

	actual := writeString(nil, value)
	if !utf8.Valid(actual) || !json.Valid(actual) {
		t.Fatalf("invalid JSON string for %q: %q", value, actual)
	}

	expected, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}

	var (
		got  string
		want string
	)

	err = json.Unmarshal(actual, &got)
	if err != nil {
		t.Fatal(err)
	}

	err = json.Unmarshal(expected, &want)
	if err != nil {
		t.Fatal(err)
	}

	if got != want {
		t.Fatalf("for %q: got %q, want %q", value, got, want)
	}
}
