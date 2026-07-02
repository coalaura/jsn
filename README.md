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

| Payload | encoding/json (v1) | encoding/json/v2 (v2) | jsn (TypedEncoder) | vs. v1 | vs. v2 |
| :--- | :--- | :--- | :--- | :--- | :--- |
| **AllTypes** | 797 ns/op (88 B/op, 6 allocs/op) | 670 ns/op (56 B/op, 4 allocs/op) | **176 ns/op (0 B/op, 0 allocs/op)** | ~4.5x | ~3.8x |
| **Small** | 219 ns/op (0 B/op, 0 allocs/op) | 184 ns/op (0 B/op, 0 allocs/op) | **72 ns/op (0 B/op, 0 allocs/op)** | ~3.0x | ~2.6x |
| **Medium** | 890 ns/op (48 B/op, 5 allocs/op) | 746 ns/op (24 B/op, 2 allocs/op) | **190 ns/op (0 B/op, 0 allocs/op)** | ~4.7x | ~3.9x |
| **Large** | 188,495 ns/op (12,886 B/op, 1,202 allocs/op) | 156,350 ns/op (4,832 B/op, 402 allocs/op) | **42,635 ns/op (0 B/op, 0 allocs/op)** | ~4.4x | ~3.7x |
| **Deep** | 2,243 ns/op (0 B/op, 0 allocs/op) | 2,271 ns/op (0 B/op, 0 allocs/op) | **677 ns/op (0 B/op, 0 allocs/op)** | ~3.3x | ~3.4x |
| **Map** | 18,058 ns/op (1,637 B/op, 203 allocs/op) | 7,375 ns/op (32 B/op, 3 allocs/op) | **2,507 ns/op (0 B/op, 0 allocs/op)** | ~7.2x | ~2.9x |
