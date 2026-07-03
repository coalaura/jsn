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

| Payload | `encoding/json` (v1) | `encoding/json/v2` | `coalaura/jsn` | vs. v1 | vs. v2 |
| :--- | ---: | ---: | ---: | ---: | ---: |
| AllTypes | 787.2ns | 655.4ns | **178.7ns** | 4.41× | 3.67× |
| Small | 223.10ns | 190.00ns | **71.30ns** | 3.13× | 2.67× |
| Medium | 885.5ns | 734.6ns | **184.8ns** | 4.79× | 3.97× |
| Large | 187.49µs | 157.48µs | **42.32µs** | 4.43× | 3.72× |
| Deep | 2.251µs | 2.246µs | **741.7ns** | 3.04× | 3.03× |
| Map | 18.134µs | 7.394µs | **2.421µs** | 7.49× | 3.05× |
| Document | 39.32µs | 37.52µs | **19.11µs** | 2.06× | 1.96× |
| **geomean** | 4.647µs | 3.670µs | **1.192µs** | **3.90×** | **3.08×** |
