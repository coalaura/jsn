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
| AllTypes | 760.2ns | 684.1ns | **175.8ns** | 4.32× | 3.89× |
| Small | 195.05ns | 200.65ns | **71.06ns** | 2.74× | 2.82× |
| Medium | 854.2ns | 753.8ns | **199.0ns** | 4.29× | 3.79× |
| Large | 183.18µs | 153.34µs | **44.53µs** | 4.11× | 3.44× |
| Deep | 2229.0ns | 2262.5ns | **729.2ns** | 3.06× | 3.10× |
| Map | 17.504µs | 6.989µs | **2.532µs** | 6.91× | 2.76× |
| Document | 38.92µs | 37.76µs | **20.15µs** | 1.93× | 1.87× |
| **geomean** | 4.462µs | 3.699µs | **1.224µs** | **3.65×** | **3.02×** |
