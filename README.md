# jsn

`jsn` is a high-performance, zero-allocation Go JSON encoder. It compiles type-level encoders (`TypedEncoder[T]`) to avoid runtime reflection and interface boxing overhead on hot paths.

## Installation

```bash
go get github.com/coalaura/jsn
```

## Usage

```go
package main

import (
	"os"
	"github.com/coalaura/jsn"
)

type User struct {
	ID    int    `json:"id"`
	Email string `json:"email"`
}

// Compile the typed encoder to eliminate runtime reflection overhead
var userEncoder = jsn.CompileTyped[User]()

func main() {
	u := User{ID: 42, Email: "joe@example.com"}

	// Instantiate encoder which reuses its internal buffer
	enc := jsn.NewEncoder(os.Stdout)

	if err := userEncoder.Encode(enc, &u); err != nil {
		panic(err)
	}
}
```

## Notes

- **Map key ordering is non-deterministic.** Unlike `encoding/json` which sorts map keys, `jsn` emits them in Go's runtime map iteration order. This is not a bug for 99% of use cases (JSON objects are unordered by spec), but byte-for-byte output comparison across runs will fail. If you need stable output, sort keys before encoding.

## Performance Benchmarks

Below is a comparison of `jsn.TypedEncoder` against Go standard libraries `encoding/json` (v1) and `encoding/json/v2` (running with `GOEXPERIMENT=jsonv2`) across various payloads:

| Payload | `encoding/json` (v1) | `encoding/json/v2` (v2) | `coalaura/jsn` (typed) | vs. v1 | vs. v2 |
| :--- | :--- | :--- | :--- | :--- | :--- |
| **AllTypes** | 800.2 ns (88 B, 6 allocs) /op | 659.3 ns (56 B, 4 allocs) /op | **182.4 ns (0 B, 0 allocs) /op** | ~4.4x | ~3.6x |
| **Small** | 225.5 ns (0 B, 0 allocs) /op | 190.5 ns (0 B, 0 allocs) /op | **74.3 ns (0 B, 0 allocs) /op** | ~3.0x | ~2.6x |
| **Medium** | 902.1 ns (48 B, 5 allocs) /op | 728.7 ns (24 B, 2 allocs) /op | **188.5 ns (0 B, 0 allocs) /op** | ~4.8x | ~3.9x |
| **Large** | 188,397 ns (12,882 B, 1,202 allocs) /op | 152,144 ns (4,832 B, 402 allocs) /op | **42,386 ns (0 B, 0 allocs) /op** | ~4.4x | ~3.6x |
| **Deep** | 2,234 ns (0 B, 0 allocs) /op | 2,254 ns (0 B, 0 allocs) /op | **688.3 ns (0 B, 0 allocs) /op** | ~3.2x | ~3.3x |
| **Map** | 18,184 ns (1,637 B, 203 allocs) /op | 7,368 ns (32 B, 3 allocs) /op | **2,522 ns (0 B, 0 allocs) /op** | ~7.2x | ~2.9x |
| **Document** | 39,204 ns (272 B, 19 allocs) /op | 37,438 ns (159 B, 12 allocs) /op | **19,218 ns (0 B, 0 allocs) /op** | ~2.0x | ~1.9x |
