package jsn

import (
	"io"
	"strings"
	"testing"
)

type stringBenchmark struct {
	name string
	text string
}

type backendBenchmark struct {
	name  string
	value any
}

type scalarRecord struct {
	Count  int64
	Total  uint64
	Active bool
	Delta  int32
	Index  uint32
	Ready  bool
}

type mixedRecord struct {
	Name        string
	Count       int64
	Active      bool
	Description string
}

var benchmarkOutput []byte

func BenchmarkStringKernel(b *testing.B) {
	cases := []stringBenchmark{
		{name: "short", text: "ordinary name"},
		{name: "ascii64", text: strings.Repeat("abcdefgh", 8)},
		{name: "ascii256", text: strings.Repeat("abcdefgh", 32)},
		{name: "ascii4096", text: strings.Repeat("abcdefgh", 512)},
		{name: "sparse", text: strings.Repeat(strings.Repeat("a", 63)+"\n", 64)},
		{name: "dense", text: strings.Repeat("a\"b\\c\n", 512)},
		{name: "html", text: strings.Repeat("<p>hello & goodbye</p>", 128)},
		{name: "unicode", text: strings.Repeat("a\xc3\xa9\xe6\x97\xa5\xf0\x9f\x98\x80", 256)},
		{name: "invalid", text: strings.Repeat("a\xff\xe2\x80\xa8", 256)},
	}

	for _, entry := range cases {
		b.Run(entry.name, func(b *testing.B) {
			buffer := make([]byte, 0, len(entry.text)*6+2)

			b.SetBytes(int64(len(entry.text)))
			b.ReportAllocs()

			for b.Loop() {
				buffer = writeString(buffer[:0], entry.text)
			}

			benchmarkOutput = buffer
		})
	}
}

func BenchmarkEncodingBackend(b *testing.B) {
	signed := make([]int64, 256)
	unsigned := make([]uint32, 256)
	floats := make([]float64, 256)
	booleans := make([]bool, 256)
	records := make([]scalarRecord, 32)
	array := new([256]int64)

	for index := range signed {
		signed[index] = int64(index*7919 - 1000000)
		unsigned[index] = uint32(index * 7919)
		floats[index] = float64(index) / 7
		booleans[index] = index%2 == 0
		array[index] = signed[index]
	}

	for index := range records {
		records[index] = scalarRecord{
			Count:  int64(index),
			Total:  uint64(index * 7919),
			Active: true,
			Delta:  -1234,
			Index:  uint32(index),
			Ready:  false,
		}
	}

	cases := []backendBenchmark{
		{name: "signed", value: signed},
		{name: "unsigned", value: unsigned},
		{name: "floats", value: floats},
		{name: "booleans", value: booleans},
		{name: "array", value: array},
		{name: "scalar_record", value: &records[0]},
		{name: "records", value: records},
		{name: "mixed_record", value: &mixedRecord{Name: "example", Count: 1234, Active: true, Description: "ordinary description"}},
	}

	for _, entry := range cases {
		b.Run(entry.name, func(b *testing.B) {
			encoder := NewEncoder(io.Discard)

			err := encoder.Encode(entry.value)
			if err != nil {
				b.Fatal(err)
			}

			b.ReportAllocs()

			for b.Loop() {
				err = encoder.Encode(entry.value)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
