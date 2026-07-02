package jsn_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/coalaura/jsn"
)

func TestEncodeMatchesStdlib(t *testing.T) {
	tests := []struct {
		name  string
		value any
		typed func(enc *jsn.Encoder) error
	}{
		{"Small", smallData, func(enc *jsn.Encoder) error {
			return smallEnc.Encode(enc, smallData)
		}},
		{"AllTypes", allTypesData, func(enc *jsn.Encoder) error {
			return allTypesEnc.Encode(enc, allTypesData)
		}},
		{"Medium", medData, func(enc *jsn.Encoder) error {
			return medEnc.Encode(enc, medData)
		}},
		{"Large", largeData, func(enc *jsn.Encoder) error {
			return largeEnc.Encode(enc, largeData)
		}},
		{"Deep", deepData, func(enc *jsn.Encoder) error {
			return deepEnc.Encode(enc, deepData)
		}},
		{"Map", mapData, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := json.Marshal(tt.value)
			if err != nil {
				t.Fatalf("encoding/json marshal: %v", err)
			}

			want := normalize(t, "encoding/json", raw)

			t.Run("Untyped", func(t *testing.T) {
				got := normalize(t, "jsn", encodeJsn(t, func(enc *jsn.Encoder) error {
					return enc.Encode(tt.value)
				}))

				assertSame(t, got, want)
			})

			if tt.typed != nil {
				t.Run("Typed", func(t *testing.T) {
					got := normalize(t, "jsn (typed)", encodeJsn(t, tt.typed))

					assertSame(t, got, want)
				})
			}
		})
	}
}

func encodeJsn(t *testing.T, encode func(enc *jsn.Encoder) error) []byte {
	t.Helper()

	var buf bytes.Buffer

	enc := jsn.NewEncoder(&buf)

	err := encode(enc)
	if err != nil {
		t.Fatalf("jsn encode: %v", err)
	}

	return buf.Bytes()
}

func normalize(t *testing.T, label string, data []byte) string {
	t.Helper()

	dec := json.NewDecoder(bytes.NewReader(data))

	dec.UseNumber()

	var value any

	err := dec.Decode(&value)
	if err != nil {
		t.Fatalf("%s produced invalid JSON: %v\nraw: %s", label, err, data)
	}

	normalized, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("re-marshal of %s output: %v", label, err)
	}

	return string(normalized)
}

func assertSame(t *testing.T, got, want string) {
	t.Helper()

	if got == want {
		return
	}

	at := firstDiff(got, want)

	t.Errorf("output differs from encoding/json at byte %d:\n got: %s\nwant: %s", at, window(got, at), window(want, at))
}

func firstDiff(a, b string) int {
	limit := min(len(a), len(b))

	for i := range limit {
		if a[i] != b[i] {
			return i
		}
	}

	if len(a) != len(b) {
		return limit
	}

	return -1
}

func window(s string, at int) string {
	start := max(at-40, 0)
	end := min(at+40, len(s))

	return s[start:end]
}
