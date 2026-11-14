package jsn_test

import (
	"encoding/json"
	json2 "encoding/json/v2"
	"reflect"
	"testing"

	"github.com/coalaura/jsn"
)

func BenchmarkDecode(b *testing.B) {
	b.Run("Small", func(b *testing.B) {
		benchmarkDecode(b, smallData)
	})

	b.Run("AllTypes", func(b *testing.B) {
		benchmarkDecode(b, allTypesData)
	})

	b.Run("Medium", func(b *testing.B) {
		benchmarkDecode(b, medData)
	})

	b.Run("Large", func(b *testing.B) {
		benchmarkDecode(b, largeData)
	})

	b.Run("Deep", func(b *testing.B) {
		benchmarkDecode(b, deepData)
	})

	b.Run("Map", func(b *testing.B) {
		benchmarkDecode(b, &mapData)
	})

	b.Run("Document", func(b *testing.B) {
		benchmarkDecode(b, docData)
	})
}

func benchmarkDecode[Value any](b *testing.B, source *Value) {
	input, err := json.Marshal(source)
	if err != nil {
		b.Fatal(err)
	}

	var want Value

	err = json.Unmarshal(input, &want)
	if err != nil {
		b.Fatal(err)
	}

	typed := jsn.CompileTypedDecoder[Value]()

	decoders := map[string]func([]byte, *Value) error{
		"Std": func(input []byte, dst *Value) error {
			return json.Unmarshal(input, dst)
		},
		"Std2": func(input []byte, dst *Value) error {
			return json2.Unmarshal(input, dst)
		},
		"Jsn": func(input []byte, dst *Value) error {
			return jsn.Unmarshal(input, dst)
		},
		"JsnTyped": typed.Unmarshal,
	}

	modes := []string{"Reuse", "Fresh"}
	implementations := []string{"Std", "Std2", "Jsn", "JsnTyped"}

	for _, mode := range modes {
		b.Run(mode, func(b *testing.B) {
			for _, name := range implementations {
				b.Run(name, func(b *testing.B) {
					decode := decoders[name]

					value := new(Value)
					err := decode(input, value)

					if err != nil || !reflect.DeepEqual(*value, want) {
						b.Fatalf("incorrect decode: %v", err)
					}

					for range benchmarkWarmup {
						err = decode(input, value)
						if err != nil {
							b.Fatal(err)
						}
					}

					b.SetBytes(int64(len(input)))
					b.ReportAllocs()

					for b.Loop() {
						if mode == "Fresh" {
							value = new(Value)
						}

						err = decode(input, value)
						if err != nil {
							b.Fatal(err)
						}
					}

					if !reflect.DeepEqual(*value, want) {
						b.Fatal("decoded value changed during benchmark")
					}
				})
			}
		})
	}
}
