package jsn

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type decodeRecord struct {
	Name   string            `json:"name"`
	Count  int64             `json:"count"`
	Active bool              `json:"active"`
	Ratio  float64           `json:"ratio"`
	Next   *decodeRecord     `json:"next"`
	List   []decodeRecord    `json:"list"`
	Map    map[string]int64  `json:"map"`
	Any    any               `json:"any"`
	Bytes  []byte            `json:"bytes"`
	Fixed  [3]int            `json:"fixed"`
	Raw    json.RawMessage   `json:"raw"`
	Time   time.Time         `json:"time"`
	Number json.Number       `json:"number"`
	Text   decodeText        `json:"text"`
	Custom decodeCustom      `json:"custom"`
	Other  map[int]string    `json:"other"`
	Nested map[string][]int  `json:"nested"`
	Values map[string]string `json:"values"`
}

type decodeFallbackRecord struct {
	decodeRecord
	Quoted int `json:"quoted,string"`
}

type decodeText string

type decodeEmptyRecord struct{}

type decodeCustom struct {
	Calls int
	Value json.RawMessage
}

type decodeEmbedded struct {
	*EmbeddedLeft
	*EmbeddedRight
	Extra int
}

type decodePrivate struct {
	*decodeRecord
}

type decodeFold struct {
	First  int `json:"ABC"`
	Second int `json:"abc"`
	Kelvin int `json:"K"`
	LongS  int `json:"S"`
}

func (value *decodeText) UnmarshalText(data []byte) error {
	*value = decodeText(strings.ToUpper(string(data)))

	return nil
}

func (value *decodeCustom) UnmarshalJSON(data []byte) error {
	value.Calls++
	value.Value = append(value.Value[:0], data...)

	return nil
}

func TestDecodeCompatibility(t *testing.T) {
	inputs := []string{
		`{}`,
		`null`,
		`{"name":"hello","count":-9223372036854775808,"active":true,"ratio":-1.5e-3}`,
		`{"ratio":1,"active":false,"count":9223372036854775807,"name":"reordered"}`,
		`{"NAME":"folded","na\u006de":"escaped","unknown":{"deep":[1,true,null,{"x":"y"}]}}`,
		`{"name":"\"\\\/\b\f\n\r\t\u0000\u2605\ud83d\ude00\ud800x\udc00"}`,
		"{\"name\":\"\xff\xfe\xe2\x82\"}",
		`{"name":null,"count":null,"active":null,"ratio":null,"next":null,"list":null,"map":null,"bytes":null}`,
		`{"next":{"name":"child","next":{"count":5}},"list":[{"count":1},{"name":"two"}]}`,
		`{"list":[],"map":{},"bytes":"","fixed":[],"any":[]}`,
		`{"map":{"a":1,"b":2,"a":3},"fixed":[1,2,3,4],"bytes":"AAEC/f7/"}`,
		`{"bytes":[0,1,254,255],"fixed":[5],"other":{"12":"value"},"nested":{"a":[1,2]}}`,
		`{"any":{"a":[1,2.5,true,false,null,"str"],"a":{"b":3}},"number":1234567890123456789}`,
		`{"raw": {"a":[1,2]},"time":"2026-07-02T01:02:21Z","text":"abc","custom":null}`,
		`{"number":"1.25e+2","custom":{"a":1},"custom":[2]}`,
		`{"next":{"name":"first"},"next":{"count":4},"map":{"a":1},"map":{"b":2}}`,
	}

	for _, input := range inputs {
		t.Run(input, func(t *testing.T) {
			checkDecode[decodeRecord](t, []byte(input))
		})
	}

	checkDecode[decodeFold](t, []byte(`{"AbC":1,"abc":2,"\u212a":3,"\u017f":4}`))
	checkDecode[decodeEmbedded](t, []byte(`{"Value":1,"Extra":3}`))
	checkDecode[decodePrivate](t, []byte(`{"name":"private"}`))
	checkDecode[map[string]*decodeRecord](t, []byte(`{"a":{"name":"first"},"a":{"count":2}}`))
	checkDecode[[]*int](t, []byte(`[1,null,3]`))
	checkDecode[[2]string](t, []byte(`["a","b","c"]`))
	checkDecode[[]decodeEmptyRecord](t, []byte(`[{},{},{}]`))
	checkDecode[map[decodeText]int](t, []byte(`{"abc":4}`))
	checkDecode[any](t, []byte(`{"x":[1,true,"x",null]}`))
	checkDecode[quotedFields](t, []byte(`{"string":"\"a\\nb\"","int":"-42","uint":"42","float":"1.25","bool":"true","ptr":"3","nil":null,"number":"123"}`))
}

func TestDecodeReusedDestinations(t *testing.T) {
	inputs := []string{
		`{"name":"initial","count":9,"list":[{"name":"old","count":1},{"name":"two"}],"fixed":[1,2,3],"map":{"old":1},"next":{"name":"retained"}}`,
		`{"list":[{"count":2}],"fixed":[4],"map":{"new":2},"next":{"count":3}}`,
		`{"list":[{},{}],"map":{},"name":null}`,
		`{"list":[],"map":null,"next":null}`,
	}

	var (
		want decodeRecord
		got  decodeRecord
	)

	for _, input := range inputs {
		err := json.Unmarshal([]byte(input), &want)
		if err != nil {
			t.Fatal(err)
		}

		err = Unmarshal([]byte(input), &got)
		if err != nil {
			t.Fatal(err)
		}

		if !reflect.DeepEqual(got, want) {
			t.Fatalf("input %s: got %#v, want %#v", input, got, want)
		}
	}
}

func TestDecodeErrors(t *testing.T) {
	inputs := []string{
		``, ` `, `nul`, `nullx`, `true false`, `01`, `-`, `1.`, `1e`, `1e+`, `[1,]`, `{"a":1,}`, `{"a" 1}`, `{"a":}`, `{"a":tru}`, `{"a":01}`, `{"a":"\x20"}`, `{"a":"\u00xz"}`, `{"a":"\u0"}`, `{"a":"unterminated}`, "\x00", "null\x00", "\"\n\"",
	}

	for _, input := range inputs {
		var value any

		want := json.Unmarshal([]byte(input), &value)
		got := Unmarshal([]byte(input), &value)

		checkDecodeError(t, got, want)

		var record decodeRecord

		got = Unmarshal([]byte(input), &record)
		checkDecodeError(t, got, want)
	}

	checkDecode[decodeRecord](t, []byte(`{"count":"bad","name":"still read","active":3}`))
	checkDecode[decodeRecord](t, []byte(`{"next":{"count":1e3},"name":"still read"}`))
	checkDecode[decodeRecord](t, []byte(`{"count":9223372036854775808}`))
	checkDecode[decodeRecord](t, []byte(`{"count":{},"fixed":true}`))
	checkDecode[int8](t, []byte(`128`))
	checkDecode[uint8](t, []byte(`256`))
	checkDecode[uint64](t, []byte(`-1`))
	checkDecode[uint64](t, []byte(`18446744073709551616`))
	checkDecode[float32](t, []byte(`1e39`))
	checkDecode[float64](t, []byte(`1e400`))
	checkDecode[decodeRecord](t, []byte(`{"BYtes":"0"}`))
	checkDecode[decodeRecord](t, []byte(`{"time":"bad","name":"after error"}`))
	checkDecode[decodeRecord](t, []byte(`{"bytes":"0","name":"after error"}`))

	invalid := []any{nil, 42, (*int)(nil)}

	for _, value := range invalid {
		want := json.Unmarshal([]byte(`1`), value)
		got := Unmarshal([]byte(`1`), value)

		checkDecodeError(t, got, want)
	}
}

func TestDecodeOwnership(t *testing.T) {
	input := []byte(`{"name":"owned","map":{"key":1},"raw":{"x":1},"list":[{"name":"esca\nped"}]}`)

	var value decodeRecord

	err := Unmarshal(input, &value)
	if err != nil {
		t.Fatal(err)
	}

	clear(input)

	if value.Name != "owned" || value.Map["key"] != 1 || string(value.Raw) != `{"x":1}` || value.List[0].Name != "esca\nped" {
		t.Fatalf("decoded data aliases input: %#v", value)
	}
}

func TestDecodeStreaming(t *testing.T) {
	reader := strings.NewReader(`[{"name":"first","any":1234567890123456789},{"name":"second"}] true`)

	decoder := NewDecoder(reader)
	decoder.UseNumber()

	token, err := decoder.Token()
	if err != nil || token != json.Delim('[') {
		t.Fatalf("opening token: %v, %v", token, err)
	}

	var (
		first  decodeRecord
		second decodeRecord
	)

	err = CompileTypedDecoder[decodeRecord]().Decode(decoder, &first)
	if err != nil || first.Any != json.Number("1234567890123456789") || !decoder.More() {
		t.Fatalf("first value: %#v, %v", first, err)
	}

	err = decoder.Decode(&second)
	if err != nil || second.Name != "second" || first.Name != "first" || decoder.More() {
		t.Fatalf("second value: %#v, %v", second, err)
	}

	token, err = decoder.Token()
	if err != nil || token != json.Delim(']') || decoder.InputOffset() <= 0 {
		t.Fatalf("closing token: %v, %v", token, err)
	}

	buffered, err := io.ReadAll(io.MultiReader(decoder.Buffered(), reader))
	if err != nil || string(buffered) != " true" {
		t.Fatalf("buffered: %q, %v", buffered, err)
	}

	decoder = NewDecoder(bytes.NewReader(buffered))

	var boolean bool

	err = decoder.Decode(&boolean)
	if err != nil || !boolean {
		t.Fatalf("boolean: %v, %v", boolean, err)
	}

	err = decoder.Decode(&boolean)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("EOF: %v", err)
	}

	decoder = NewDecoder(strings.NewReader(`{"unknown":1,"count":2}`))
	decoder.DisallowUnknownFields()

	err = decoder.Decode(&first)
	if err == nil || err.Error() != `json: unknown field "unknown"` || first.Count != 2 {
		t.Fatalf("unknown field: %#v, %v", first, err)
	}
}

func TestDecodeStreamErrors(t *testing.T) {
	input := " \nnull \n \"wrong\" 42"

	standard := json.NewDecoder(strings.NewReader(input))
	decoder := NewDecoder(strings.NewReader(input))
	typedDecoder := NewDecoder(strings.NewReader(input))
	typed := CompileTypedDecoder[int]()

	var (
		expected int
		dynamic  int
		compiled int
	)

	for range 3 {
		want := standard.Decode(&expected)
		got := decoder.Decode(&dynamic)
		checkDecodeError(t, got, want)

		got = typed.Decode(typedDecoder, &compiled)
		checkDecodeError(t, got, want)

		if dynamic != expected || compiled != expected || decoder.InputOffset() != standard.InputOffset() || typedDecoder.InputOffset() != standard.InputOffset() {
			t.Fatal("stream value or offset differs after decoding error")
		}
	}
}

func TestDecodeDepthAndConcurrency(t *testing.T) {
	input := []byte(strings.Repeat("[", maxDecodeDepth+1) + "0" + strings.Repeat("]", maxDecodeDepth+1))

	var value any

	err := Unmarshal(input, &value)

	_, ok := errors.AsType[*json.SyntaxError](err)
	if !ok {
		t.Fatalf("depth limit: %v", err)
	}

	var workers sync.WaitGroup

	for range 16 {
		workers.Go(func() {
			var record decodeRecord

			err := CompileTypedDecoder[decodeRecord]().Unmarshal([]byte(`{"next":{"count":5}}`), &record)
			if err != nil || record.Next == nil || record.Next.Count != 5 {
				t.Errorf("concurrent decode: %#v, %v", record, err)
			}
		})
	}

	workers.Wait()
}

func TestDecodeFallback(t *testing.T) {
	inputs := []string{`{"quoted":"42","name":"ok"}`, `{"quoted":42}`, `{"quoted":"bad"}`, `{"quoted":"9223372036854775808"}`, `{"quoted":"bad","name":"after error"}`}

	for _, input := range inputs {
		checkDecode[decodeFallbackRecord](t, []byte(input))
	}
}

func FuzzDecode(f *testing.F) {
	seeds := []string{`null`, `{}`, `[]`, `{"count":1,"name":"hello"}`, `{"any":{"a":[1,true,null]}}`, `"\ud800\udc00"`, `1e400`, `{"next":{"count":"bad"}}`, `{"bytes":"AQI="}`, `{"BYtes":"0"}`, `{"list":[{"count":"bad"}],"text":3}`, `{"map":{"a":"bad"}}`}

	for _, seed := range seeds {
		f.Add([]byte(seed))
	}

	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > 4096 {
			return
		}

		checkDecode[any](t, input)

		if json.Valid(input) {
			checkDecode[decodeRecord](t, input)
		} else {
			var record decodeRecord

			err := Unmarshal(input, &record)
			if err == nil {
				t.Fatal("accepted invalid JSON")
			}
		}
	})
}

func checkDecode[Value any](t *testing.T, input []byte) {
	t.Helper()

	var (
		want  Value
		got   Value
		typed Value
	)

	wantError := json.Unmarshal(input, &want)
	gotError := Unmarshal(input, &got)
	typedError := CompileTypedDecoder[Value]().Unmarshal(input, &typed)

	if (wantError == nil) != (gotError == nil) || (wantError == nil) != (typedError == nil) {
		t.Fatalf("%q: errors: std=%v, dynamic=%v, typed=%v", input, wantError, gotError, typedError)
	}

	if wantError == nil || json.Valid(input) {
		if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(typed, want) {
			t.Fatalf("%q: got %#v, typed %#v, want %#v", input, got, typed, want)
		}
	}

	_, ok := errors.AsType[*json.UnmarshalTypeError](wantError)
	if ok {
		checkDecodeError(t, gotError, wantError)
		checkDecodeError(t, typedError, wantError)
	}

	if wantError == nil {
		stream := NewDecoder(bytes.NewReader(input))

		var streamed Value

		err := stream.Decode(&streamed)
		if err != nil || !reflect.DeepEqual(streamed, want) {
			t.Fatalf("stream: %#v, %v; want %#v", streamed, err, want)
		}
	}
}

func checkDecodeError(t *testing.T, got, want error) {
	t.Helper()

	if got == nil || want == nil {
		if got != nil || want != nil {
			t.Fatalf("errors: got %v, want %v", got, want)
		}

		return
	}

	if reflect.TypeOf(got) != reflect.TypeOf(want) || got.Error() != want.Error() {
		t.Fatalf("errors: got %#v, want %#v", got, want)
	}

	wantSyntax, ok := errors.AsType[*json.SyntaxError](want)
	if ok {
		gotSyntax, _ := errors.AsType[*json.SyntaxError](got)
		if gotSyntax.Offset != wantSyntax.Offset {
			t.Fatalf("syntax offsets: got %d, want %d", gotSyntax.Offset, wantSyntax.Offset)
		}
	}

	wantType, ok := errors.AsType[*json.UnmarshalTypeError](want)
	if ok {
		gotType, _ := errors.AsType[*json.UnmarshalTypeError](got)
		if gotType.Offset != wantType.Offset || gotType.Type != wantType.Type || gotType.Value != wantType.Value || gotType.Struct != wantType.Struct || gotType.Field != wantType.Field {
			t.Fatalf("type errors: got %#v, want %#v", gotType, wantType)
		}
	}
}
