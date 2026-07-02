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
	u := User{ID: 42, Email: "laura@example.com"}

	// Instantiate encoder which reuses its internal buffer
	enc := jsn.NewEncoder(os.Stdout)

	if err := userEncoder.Encode(enc, &u); err != nil {
		panic(err)
	}
}
```

## Performance Benchmarks

Below is a comparison against the Go standard library `encoding/json` across different payloads:

| Payload | encoding/json (Standard Library) | jsn (TypedEncoder) | Speedup |
| :--- | :--- | :--- | :--- |
| **Small** | 152 ns/op (0 B/op, 0 allocs/op) | **74 ns/op (0 B/op, 0 allocs/op)** | ~2.1x |
| **AllTypes** | 637 ns/op (192 B/op, 6 allocs/op) | **186 ns/op (0 B/op, 0 allocs/op)** | ~3.4x |
| **Medium** | 768 ns/op (248 B/op, 8 allocs/op) | **182 ns/op (0 B/op, 0 allocs/op)** | ~4.2x |
| **Large** | 180,659 ns/op (64,400 B/op, 2,001 allocs/op) | **41,091 ns/op (0 B/op, 0 allocs/op)** | ~4.4x |
| **Map** | 24,636 ns/op (13,011 B/op, 401 allocs/op) | **2,397 ns/op (0 B/op, 0 allocs/op)** | ~10.2x |
