package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestReport(t *testing.T) {
	directory := t.TempDir()
	paths := make([]string, len(labels))
	data := "goos: linux\nBenchmarkEncode_Small_Std-8 100 10 ns/op 100 MB/s 0 B/op 0 allocs/op\nBenchmarkEncode_Small_Std-8 100 30 ns/op 100 MB/s 8 B/op 1 allocs/op\nPASS\n"

	for index := range paths {
		paths[index] = filepath.Join(directory, strconv.Itoa(index)+".txt")

		err := os.WriteFile(paths[index], []byte(data), 0o600)
		if err != nil {
			t.Fatal(err)
		}
	}

	var output bytes.Buffer

	err := run(&output, paths)
	if err != nil {
		t.Fatal(err)
	}

	expectations := []string{"| Small | 20.00 | 20.00 | 20.00 | 20.00 |", "10.00-30.00", "8 / 1", "2 samples per cell"}

	for _, expected := range expectations {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("missing %q in report: %s", expected, &output)
		}
	}

	err = os.WriteFile(paths[0], []byte("BenchmarkEncode_Small_Std-8 100 10 ns/op 0 B/op 0 allocs/op\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	err = run(&output, paths)
	if err == nil {
		t.Fatal("accepted an incomplete run")
	}
}

func TestRejectInvalidMetrics(t *testing.T) {
	cases := []string{
		"BenchmarkEncode_Small_Std-8 100 NaN ns/op 0 B/op 0 allocs/op",
		"BenchmarkEncode_Small_Std-8 100 10 ns/op 0 B/op",
		"BenchmarkEncode_Small_Std-8 100 -1 ns/op 0 B/op 0 allocs/op",
		"BenchmarkEncode_Small_Std-8 100 10 ns/op 0 B/op 0",
	}

	for _, data := range cases {
		_, err := parse(strings.NewReader(data))
		if err == nil {
			t.Fatalf("accepted malformed sample: %s", data)
		}
	}
}
