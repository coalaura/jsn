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

## Performance Benchmarks

Below is a comparison of `jsn.TypedEncoder` against Go standard libraries `encoding/json` (v1) and `encoding/json/v2` (running with `GOEXPERIMENT=jsonv2`) across various payloads:

| Payload | `encoding/json` (v1) | `encoding/json/v2` (v2) | `coalaura/jsn` (typed) | vs. v1 | vs. v2 |
| :--- | :--- | :--- | :--- | :--- | :--- |
| **AllTypes** | 800.2 ns/op (88 B/op, 6 allocs/op) | 659.3 ns/op (56 B/op, 4 allocs/op) | **180.0 ns/op (0 B/op, 0 allocs/op)** | ~4.4x | ~3.7x |
| **Small** | 225.5 ns/op (0 B/op, 0 allocs/op) | 190.5 ns/op (0 B/op, 0 allocs/op) | **71.3 ns/op (0 B/op, 0 allocs/op)** | ~3.2x | ~2.7x |
| **Medium** | 902.1 ns/op (48 B/op, 5 allocs/op) | 728.7 ns/op (24 B/op, 2 allocs/op) | **174.9 ns/op (0 B/op, 0 allocs/op)** | ~5.2x | ~4.2x |
| **Large** | 188,397 ns/op (12,882 B/op, 1,202 allocs/op) | 152,144 ns/op (4,832 B/op, 402 allocs/op) | **39,206 ns/op (0 B/op, 0 allocs/op)** | ~4.8x | ~3.9x |
| **Deep** | 2,234 ns/op (0 B/op, 0 allocs/op) | 2,254 ns/op (0 B/op, 0 allocs/op) | **653.1 ns/op (0 B/op, 0 allocs/op)** | ~3.4x | ~3.5x |
| **Map** | 18,184 ns/op (1,637 B/op, 203 allocs/op) | 7,368 ns/op (32 B/op, 3 allocs/op) | **2,288 ns/op (0 B/op, 0 allocs/op)** | ~7.9x | ~3.2x |
| **Document** | 39,204 ns/op (272 B/op, 19 allocs/op) | 37,438 ns/op (159 B/op, 12 allocs/op) | **18,508 ns/op (0 B/op, 0 allocs/op)** | ~2.1x | ~2.0x |
