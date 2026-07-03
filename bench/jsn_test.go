package jsn_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/coalaura/jsn"
)

func roundtripStd(t *testing.T, v any) any {
	t.Helper()

	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("stdlib marshal: %v", err)
	}

	var out any

	dec := json.NewDecoder(bytes.NewReader(raw))

	dec.UseNumber()

	err = dec.Decode(&out)
	if err != nil {
		t.Fatalf("stdlib decode own output: %v", err)
	}

	return out
}

func roundtripJsn(t *testing.T, v any) any {
	t.Helper()

	var buf bytes.Buffer

	enc := jsn.NewEncoder(&buf)

	err := enc.Encode(v)
	if err != nil {
		t.Fatalf("jsn encode: %v", err)
	}

	var out any

	dec := json.NewDecoder(&buf)

	dec.UseNumber()

	err = dec.Decode(&out)
	if err != nil {
		t.Fatalf("decode jsn output: %v\nraw: %s", err, buf.Bytes())
	}

	return out
}

func roundtripJsnTyped[T any](t *testing.T, te *jsn.TypedEncoder[T], v *T) any {
	t.Helper()

	var buf bytes.Buffer

	enc := jsn.NewEncoder(&buf)

	err := te.Encode(enc, v)
	if err != nil {
		t.Fatalf("jsn typed encode: %v", err)
	}

	var out any

	dec := json.NewDecoder(&buf)

	dec.UseNumber()

	err = dec.Decode(&out)
	if err != nil {
		t.Fatalf("decode jsn typed output: %v\nraw: %s", err, buf.Bytes())
	}

	return out
}

func assertEqual(t *testing.T, label string, got, want any) {
	t.Helper()

	gotRaw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal got: %v", err)
	}

	wantRaw, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal want: %v", err)
	}

	if !bytes.Equal(gotRaw, wantRaw) {
		t.Errorf("%s mismatch:\n got: %s\nwant: %s", label, gotRaw, wantRaw)
	}
}

func TestScalars(t *testing.T) {
	t.Run("IntMinMax", func(t *testing.T) {
		vals := []int64{math.MinInt64, math.MaxInt64, -1, 0, 1}

		for _, v := range vals {
			got := roundtripJsn(t, v)
			want := roundtripStd(t, v)

			assertEqual(t, "int64", got, want)
		}
	})

	t.Run("UintMax", func(t *testing.T) {
		v := uint64(math.MaxUint64)

		assertEqual(t, "uint64", roundtripJsn(t, v), roundtripStd(t, v))
	})

	t.Run("FloatSpecial", func(t *testing.T) {
		vals := []float64{0, -0.0, 1e-10, 1e20, 1.7976931348623157e308, -1.7976931348623157e308, 3.141592653589793}

		for _, v := range vals {
			assertEqual(t, "float64", roundtripJsn(t, v), roundtripStd(t, v))
		}

		for _, v := range []float32{0, 1.5, -1.5, 3.14159} {
			assertEqual(t, "float32", roundtripJsn(t, v), roundtripStd(t, v))
		}
	})

	t.Run("Bool", func(t *testing.T) {
		assertEqual(t, "true", roundtripJsn(t, true), roundtripStd(t, true))
		assertEqual(t, "false", roundtripJsn(t, false), roundtripStd(t, false))
	})
}

func TestFloatErrors(t *testing.T) {
	for _, v := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		var buf bytes.Buffer

		enc := jsn.NewEncoder(&buf)

		err := enc.Encode(v)
		if err == nil {
			t.Errorf("expected error for %v, got: %s", v, buf.Bytes())
		}
	}
}

func TestStringEscaping(t *testing.T) {
	cases := []string{
		"",
		"plain ascii",
		`back\slash`,
		`quote"`,
		"tab\there",
		"newline\nhere",
		"cr\rhere",
		"control\x00\x01\x1f\x7f",
		"unicode star \u2605",
		"emoji \U0001F600",
		"line sep \u2028 here",
		"para sep \u2029 here",
		"html <>&",
		"mixed \"quotes\" \n\t \u2605 \U0001F600 <>&",
		strings.Repeat("a", 200),
		strings.Repeat("a", 200) + "\u2028" + strings.Repeat("b", 200),
		strings.Repeat("escape\"\\", 100),
	}

	for _, s := range cases {
		got := roundtripJsn(t, s)
		want := roundtripStd(t, s)

		assertEqual(t, "string", got, want)
	}
}

func TestNilAndEmpty(t *testing.T) {
	t.Run("NilSlice", func(t *testing.T) {
		var s []int

		assertEqual(t, "nil slice", roundtripJsn(t, s), roundtripStd(t, s))
	})

	t.Run("EmptySlice", func(t *testing.T) {
		s := []int{}

		assertEqual(t, "empty slice", roundtripJsn(t, s), roundtripStd(t, s))
	})

	t.Run("NilMap", func(t *testing.T) {
		var m map[string]int

		assertEqual(t, "nil map", roundtripJsn(t, m), roundtripStd(t, m))
	})

	t.Run("EmptyMap", func(t *testing.T) {
		m := map[string]int{}

		assertEqual(t, "empty map", roundtripJsn(t, m), roundtripStd(t, m))
	})

	t.Run("NilByteSlice", func(t *testing.T) {
		var b []byte

		assertEqual(t, "nil []byte", roundtripJsn(t, b), roundtripStd(t, b))
	})

	t.Run("EmptyByteSlice", func(t *testing.T) {
		b := []byte{}

		assertEqual(t, "empty []byte", roundtripJsn(t, b), roundtripStd(t, b))
	})

	t.Run("NilRawMessage", func(t *testing.T) {
		var rm json.RawMessage

		assertEqual(t, "nil raw", roundtripJsn(t, rm), roundtripStd(t, rm))
	})

	t.Run("EmptyRawMessage", func(t *testing.T) {
		rm := json.RawMessage(`{}`)

		assertEqual(t, "empty raw", roundtripJsn(t, rm), roundtripStd(t, rm))
	})
}

func TestPointers(t *testing.T) {
	t.Run("NilPointer", func(t *testing.T) {
		var p *int

		assertEqual(t, "nil *int", roundtripJsn(t, p), roundtripStd(t, p))
	})

	t.Run("NonNullPointer", func(t *testing.T) {
		v := 42
		p := &v

		assertEqual(t, "*int", roundtripJsn(t, p), roundtripStd(t, p))
	})

	t.Run("NilPointerField", func(t *testing.T) {
		type S struct {
			P *int `json:"p"`
		}

		assertEqual(t, "nil ptr field", roundtripJsn(t, S{}), roundtripStd(t, S{}))
	})

	t.Run("DoublePointer", func(t *testing.T) {
		v := 7
		p := &v
		pp := &p

		assertEqual(t, "**int", roundtripJsn(t, pp), roundtripStd(t, pp))
	})
}

func TestArrays(t *testing.T) {
	t.Run("ScalarArray", func(t *testing.T) {
		a := [5]int{1, 2, 3, 4, 5}

		assertEqual(t, "[5]int", roundtripJsn(t, a), roundtripStd(t, a))
	})

	t.Run("BoolArray", func(t *testing.T) {
		a := [4]bool{true, false, true, false}

		assertEqual(t, "[4]bool", roundtripJsn(t, a), roundtripStd(t, a))
	})

	t.Run("EmptyArray", func(t *testing.T) {
		a := [0]int{}

		assertEqual(t, "[0]int", roundtripJsn(t, a), roundtripStd(t, a))
	})

	t.Run("StructArray", func(t *testing.T) {
		type P struct {
			X int `json:"x"`
		}

		a := [3]P{{1}, {2}, {3}}

		assertEqual(t, "[3]P", roundtripJsn(t, a), roundtripStd(t, a))
	})
}

func TestMaps(t *testing.T) {
	t.Run("MapStringString", func(t *testing.T) {
		m := map[string]string{"a": "1", "b": "2", "c": "3"}

		assertEqual(t, "map[str,str]", roundtripJsn(t, m), roundtripStd(t, m))
	})

	t.Run("MapStringInt64", func(t *testing.T) {
		m := map[string]int64{"a": 1, "b": -2, "c": math.MaxInt64}

		assertEqual(t, "map[str,i64]", roundtripJsn(t, m), roundtripStd(t, m))
	})

	t.Run("MapStringUint64", func(t *testing.T) {
		m := map[string]uint64{"a": 1, "b": math.MaxUint64}

		assertEqual(t, "map[str,u64]", roundtripJsn(t, m), roundtripStd(t, m))
	})

	t.Run("MapStringFloat64", func(t *testing.T) {
		m := map[string]float64{"a": 1.5, "b": -0.5, "c": 3.14159}

		assertEqual(t, "map[str,f64]", roundtripJsn(t, m), roundtripStd(t, m))
	})

	t.Run("MapStringBool", func(t *testing.T) {
		m := map[string]bool{"a": true, "b": false}

		assertEqual(t, "map[str,bool]", roundtripJsn(t, m), roundtripStd(t, m))
	})

	t.Run("MapStringAny", func(t *testing.T) {
		m := map[string]any{"s": "str", "n": 42, "f": 1.5, "b": true, "nil": nil, "arr": []int{1, 2}}

		assertEqual(t, "map[str,any]", roundtripJsn(t, m), roundtripStd(t, m))
	})

	t.Run("MapIntString", func(t *testing.T) {
		m := map[int]string{1: "a", 2: "b"}

		assertEqual(t, "map[int,str]", roundtripJsn(t, m), roundtripStd(t, m))
	})
}

type OmitStruct struct {
	Str   string         `json:"str,omitempty"`
	Int   int            `json:"int,omitempty"`
	Ptr   *int           `json:"ptr,omitempty"`
	Slice []int          `json:"slice,omitempty"`
	Map   map[string]int `json:"map,omitempty"`
	Time  time.Time      `json:"time,omitempty"`
	Keep  string         `json:"keep"`
}

type OmitZeroStruct struct {
	Str  string    `json:"str,omitzero"`
	Int  int       `json:"int,omitzero"`
	Time time.Time `json:"time,omitzero"`
	Keep string    `json:"keep"`
}

type IsZeroerImpl struct {
	Empty bool
}

func (z IsZeroerImpl) IsZero() bool {
	return z.Empty
}

type OmitZeroIsZeroer struct {
	Val  IsZeroerImpl `json:"val,omitzero"`
	Keep string       `json:"keep"`
}

func TestOmitEmpty(t *testing.T) {
	v := OmitStruct{Keep: "yes"}

	assertEqual(t, "omitempty empty", roundtripJsn(t, v), roundtripStd(t, v))

	x := 5

	v2 := OmitStruct{
		Str:   "s",
		Int:   5,
		Ptr:   &x,
		Slice: []int{1},
		Map:   map[string]int{"k": 1},
		Time:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Keep:  "yes",
	}

	assertEqual(t, "omitempty full", roundtripJsn(t, v2), roundtripStd(t, v2))
}

func TestOmitZero(t *testing.T) {
	v := OmitZeroStruct{Keep: "yes"}

	assertEqual(t, "omitzero empty", roundtripJsn(t, v), roundtripStd(t, v))

	v2 := OmitZeroStruct{
		Str:  "s",
		Int:  5,
		Time: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Keep: "yes",
	}

	assertEqual(t, "omitzero full", roundtripJsn(t, v2), roundtripStd(t, v2))
}

func TestOmitZeroIsZeroer(t *testing.T) {
	v := OmitZeroIsZeroer{Val: IsZeroerImpl{Empty: true}, Keep: "yes"}

	assertEqual(t, "iszeroer empty", roundtripJsn(t, v), roundtripStd(t, v))

	v2 := OmitZeroIsZeroer{Val: IsZeroerImpl{Empty: false}, Keep: "yes"}

	assertEqual(t, "iszeroer nonempty", roundtripJsn(t, v2), roundtripStd(t, v2))
}

func TestEmbedded(t *testing.T) {
	v := embedData

	assertEqual(t, "embedded untyped", roundtripJsn(t, *v), roundtripStd(t, *v))
	assertEqual(t, "embedded typed", roundtripJsnTyped(t, embedEnc, v), roundtripStd(t, *v))
}

func TestTime(t *testing.T) {
	t.Run("ZeroTime", func(t *testing.T) {
		var tm time.Time

		assertEqual(t, "zero time", roundtripJsn(t, tm), roundtripStd(t, tm))
	})

	t.Run("ValidTime", func(t *testing.T) {
		tm := time.Date(2026, 7, 3, 12, 30, 45, 123456789, time.UTC)

		assertEqual(t, "valid time", roundtripJsn(t, tm), roundtripStd(t, tm))
	})

	t.Run("TimeWithTimezone", func(t *testing.T) {
		loc, _ := time.LoadLocation("America/New_York")
		tm := time.Date(2026, 7, 3, 8, 30, 0, 0, loc)

		assertEqual(t, "tz time", roundtripJsn(t, tm), roundtripStd(t, tm))
	})

	t.Run("NanoOnly", func(t *testing.T) {
		tm := time.Date(2026, 7, 3, 12, 30, 45, 100, time.UTC)

		assertEqual(t, "nano time", roundtripJsn(t, tm), roundtripStd(t, tm))
	})

	t.Run("OutOfRange", func(t *testing.T) {
		tm := time.Date(-1, 1, 1, 0, 0, 0, 0, time.UTC)

		var buf bytes.Buffer

		enc := jsn.NewEncoder(&buf)

		err := enc.Encode(tm)
		if err == nil {
			t.Errorf("expected error for out-of-range time, got: %s", buf.Bytes())
		}
	})
}

type ValByteMarshaler struct {
	V string
}

func (v ValByteMarshaler) MarshalJSON() ([]byte, error) {
	return []byte(`{"val":"` + v.V + `"}`), nil
}

type WriterMarshalerImpl struct {
	V string
}

func (w WriterMarshalerImpl) MarshalJSONTo(wr io.Writer) error {
	_, err := io.WriteString(wr, `{"w":"`+w.V+`"}`)

	return err
}

func (w WriterMarshalerImpl) MarshalJSON() ([]byte, error) {
	return []byte(`{"w":"` + w.V + `"}`), nil
}

type ErrMarshaler struct{}

func (ErrMarshaler) MarshalJSON() ([]byte, error) {
	return nil, errors.New("marshal failed")
}

func TestMarshalers(t *testing.T) {
	t.Run("PtrByteMarshaler", func(t *testing.T) {
		v := &PtrByteMarshaler{Value: "hello"}

		assertEqual(t, "ptr byte", roundtripJsn(t, v), roundtripStd(t, v))
	})

	t.Run("ValByteMarshaler", func(t *testing.T) {
		v := ValByteMarshaler{V: "world"}

		assertEqual(t, "val byte", roundtripJsn(t, v), roundtripStd(t, v))
	})

	t.Run("WriterMarshaler", func(t *testing.T) {
		v := WriterMarshalerImpl{V: "stream"}

		assertEqual(t, "writer", roundtripJsn(t, v), roundtripStd(t, v))
	})

	t.Run("MarshalError", func(t *testing.T) {
		var buf bytes.Buffer

		enc := jsn.NewEncoder(&buf)

		err := enc.Encode(ErrMarshaler{})
		if err == nil {
			t.Errorf("expected marshal error, got: %s", buf.Bytes())
		}
	})
}

func TestInterface(t *testing.T) {
	t.Run("NilInterface", func(t *testing.T) {
		var v any

		assertEqual(t, "nil any", roundtripJsn(t, v), roundtripStd(t, v))
	})

	t.Run("InterfaceValues", func(t *testing.T) {
		vals := []any{42, "str", true, 3.14, []int{1, 2, 3}, map[string]int{"a": 1}, nil}

		for _, v := range vals {
			assertEqual(t, "any", roundtripJsn(t, v), roundtripStd(t, v))
		}
	})

	t.Run("NestedInterface", func(t *testing.T) {
		m := map[string]any{
			"obj":  map[string]any{"k": "v"},
			"arr":  []any{1, "two", true},
			"num":  42,
			"null": nil,
		}

		assertEqual(t, "nested any", roundtripJsn(t, m), roundtripStd(t, m))
	})
}

func TestTypedNil(t *testing.T) {
	enc := jsn.CompileTyped[*Small]()

	var buf bytes.Buffer

	e := jsn.NewEncoder(&buf)

	err := enc.Encode(e, nil)
	if err != nil {
		t.Fatalf("typed nil encode: %v", err)
	}

	if !bytes.Equal(bytes.TrimSpace(buf.Bytes()), []byte("null")) {
		t.Errorf("typed nil: got %s, want null", buf.Bytes())
	}
}

func TestRecursive(t *testing.T) {
	t.Run("NilRoot", func(t *testing.T) {
		var n *Node

		assertEqual(t, "nil node", roundtripJsn(t, n), roundtripStd(t, n))
	})

	t.Run("DeepChain", func(t *testing.T) {
		assertEqual(t, "deep chain", roundtripJsn(t, deepData), roundtripStd(t, deepData))
	})

	t.Run("EmptyChildren", func(t *testing.T) {
		n := &Node{Name: "leaf", Value: 1}

		assertEqual(t, "leaf node", roundtripJsn(t, n), roundtripStd(t, n))
	})
}

func TestTypedMatchesUntyped(t *testing.T) {
	cases := []struct {
		name  string
		typed func() any
	}{
		{"Small", func() any { return *smallData }},
		{"AllTypes", func() any { return *allTypesData }},
		{"Medium", func() any { return *medData }},
		{"Document", func() any { return *docData }},
		{"WithRaw", func() any { return *rawData }},
		{"WithEmbed", func() any { return *embedData }},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			want := roundtripStd(t, c.typed())
			untyped := roundtripJsn(t, c.typed())

			assertEqual(t, "untyped vs stdlib", untyped, want)
		})
	}
}

type errWriter struct{}

func (errWriter) Write(p []byte) (int, error) {
	return 0, errors.New("write failed")
}

func TestStreaming(t *testing.T) {
	t.Run("LargeOutputFlushes", func(t *testing.T) {
		s := make([]string, 1000)

		for i := range s {
			s[i] = strings.Repeat("x", 100)
		}

		assertEqual(t, "large slice", roundtripJsn(t, s), roundtripStd(t, s))
	})

	t.Run("WriteError", func(t *testing.T) {
		enc := jsn.NewEncoder(errWriter{})

		err := enc.Encode(smallData)
		if err == nil {
			t.Error("expected write error")
		}
	})
}

func TestAllOmitEmpty(t *testing.T) {
	type S struct {
		A string `json:"a,omitempty"`
		B int    `json:"b,omitempty"`
	}

	v := S{}

	assertEqual(t, "all omitempty empty", roundtripJsn(t, v), roundtripStd(t, v))
}

func TestSkipField(t *testing.T) {
	type S struct {
		Public  string `json:"public"`
		private string
		Hidden  string `json:"-"`
		Shown   string `json:"shown"`
	}

	v := S{Public: "p", private: "x", Hidden: "h", Shown: "s"}

	assertEqual(t, "skip field", roundtripJsn(t, v), roundtripStd(t, v))
}
