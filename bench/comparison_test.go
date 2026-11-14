package jsn_test

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	json2 "encoding/json/v2"
	"io"
	"reflect"
	"testing"
	"unicode/utf8"

	"github.com/coalaura/jsn"
)

const benchmarkWarmup = 100

func BenchmarkEncode_AllTypes_Jsn(b *testing.B) {
	benchmarkDynamic(b, allTypesData)
}

func BenchmarkEncode_Small_Jsn(b *testing.B) {
	benchmarkDynamic(b, smallData)
}

func BenchmarkEncode_Medium_Jsn(b *testing.B) {
	benchmarkDynamic(b, medData)
}

func BenchmarkEncode_Large_Jsn(b *testing.B) {
	benchmarkDynamic(b, largeData)
}

func BenchmarkEncode_Deep_Jsn(b *testing.B) {
	benchmarkDynamic(b, deepData)
}

func BenchmarkEncode_Document_Jsn(b *testing.B) {
	benchmarkDynamic(b, docData)
}

func BenchmarkEncode_Map_JsnTyped(b *testing.B) {
	benchmarkTyped(b, &mapData, jsn.CompileTyped[map[string]int64]())
}

func benchmarkV1(b *testing.B, value any) {
	var output bytes.Buffer

	encoder := json.NewEncoder(&output)

	err := encoder.Encode(value)
	checkEncoding(b, value, output.Bytes(), err)

	encoder = json.NewEncoder(io.Discard)

	for range benchmarkWarmup {
		err = encoder.Encode(value)
		if err != nil {
			b.Fatal(err)
		}
	}

	b.ReportAllocs()

	for b.Loop() {
		err = encoder.Encode(value)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func benchmarkV2(b *testing.B, value any) {
	var output bytes.Buffer
	// Match v1/jsn HTML and JavaScript escaping. Map order stays unspecified.
	encoder := jsontext.NewEncoder(&output, jsontext.EscapeForHTML(true), jsontext.EscapeForJS(true))

	err := json2.MarshalEncode(encoder, value)
	checkEncoding(b, value, output.Bytes(), err)

	encoder.Reset(io.Discard, jsontext.EscapeForHTML(true), jsontext.EscapeForJS(true))

	for range benchmarkWarmup {
		err = json2.MarshalEncode(encoder, value)
		if err != nil {
			b.Fatal(err)
		}
	}

	b.ReportAllocs()

	for b.Loop() {
		err = json2.MarshalEncode(encoder, value)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func benchmarkDynamic(b *testing.B, value any) {
	var output bytes.Buffer

	encoder := jsn.NewEncoder(&output)

	err := encoder.Encode(value)
	checkEncoding(b, value, output.Bytes(), err)

	encoder = jsn.NewEncoder(io.Discard)

	for range benchmarkWarmup {
		err = encoder.Encode(value)
		if err != nil {
			b.Fatal(err)
		}
	}

	b.ReportAllocs()

	for b.Loop() {
		err = encoder.Encode(value)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func benchmarkTyped[Value any](b *testing.B, value *Value, typed *jsn.TypedEncoder[Value]) {
	var output bytes.Buffer

	encoder := jsn.NewEncoder(&output)

	err := typed.Encode(encoder, value)
	checkEncoding(b, value, output.Bytes(), err)

	encoder = jsn.NewEncoder(io.Discard)

	for range benchmarkWarmup {
		err = typed.Encode(encoder, value)
		if err != nil {
			b.Fatal(err)
		}
	}

	b.ReportAllocs()

	for b.Loop() {
		err = typed.Encode(encoder, value)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func checkEncoding(b *testing.B, value any, output []byte, err error) {
	b.Helper()

	if err != nil {
		b.Fatal(err)
	}

	if !utf8.Valid(output) || !json.Valid(output) || !bytes.HasSuffix(output, []byte{'\n'}) {
		b.Fatal("encoder produced invalid UTF-8, invalid JSON, or missing newline")
	}

	expected, err := json.Marshal(value)
	if err != nil {
		b.Fatal(err)
	}

	if len(output) != len(expected)+1 {
		b.Fatalf("output size differs: got %d bytes, want %d including newline", len(output), len(expected)+1)
	}

	var (
		want any
		got  any
	)

	wantDecoder := json.NewDecoder(bytes.NewReader(expected))
	wantDecoder.UseNumber()

	err = wantDecoder.Decode(&want)
	if err != nil {
		b.Fatal(err)
	}

	gotDecoder := json.NewDecoder(bytes.NewReader(output))
	gotDecoder.UseNumber()

	err = gotDecoder.Decode(&got)
	if err != nil {
		b.Fatal(err)
	}

	if !reflect.DeepEqual(want, got) {
		b.Fatal("encoded value differs from encoding/json")
	}

	// Every implementation reports the same logical payload size, excluding
	// the framing newline. This remains comparable when map order differs.
	b.SetBytes(int64(len(expected)))
}
