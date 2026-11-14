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

Median time per encode, measured on 2026-11-14 with Go 1.27.4, Windows/amd64 (`GOAMD64=v1`, default `GOEXPERIMENT`) and an AMD Ryzen 9 9950X3D. Each cell contains 12 one-second samples from the same compiled benchmark binary, with library execution order rotated each round. Every sample performs 100 untimed warm-up encodes before measurement.

| Payload | `encoding/json` | `encoding/json/v2` | jsn `Encoder.Encode` | jsn `TypedEncoder.Encode` |
| :--- | ---: | ---: | ---: | ---: |
| AllTypes | 785.30 ns | 672.80 ns | 187.30 ns | 173.50 ns |
| Small | 211.45 ns | 200.05 ns | 90.83 ns | 75.31 ns |
| Medium | 927.90 ns | 789.05 ns | 225.20 ns | 206.70 ns |
| Large | 204.63 us | 175.62 us | 48.31 us | 48.06 us |
| Deep | 2.36 us | 2.52 us | 992.55 ns | 778.80 ns |
| Map | 19.40 us | 8.02 us | 2.50 us | 2.64 us |
| Document | 43.05 us | 43.80 us | 22.56 us | 22.36 us |

Both jsn APIs reported **0 B/op and 0 allocs/op in every sample**. These are warmed, steady-state measurements into `io.Discard`, reusing heap-resident input values and encoder buffers. Encoder construction, type compilation and fixture validation are outside the timed loops; these numbers do not measure first-use cost or fresh-input allocations.

All implementations enable HTML and JavaScript escaping. Each benchmark validates UTF-8, JSON content, output length and newline framing before timing and checks errors during warm-up and measurement. `encoding/json` sorts map keys; jsn and the configured v2 encoder do not, so map-containing rows include that additional work for `encoding/json`.

Run the comparison from the repository root with Go 1.27 or later:

```bash
bash bench/bench.sh
```

The runner tests the suite, rebuilds once and reruns every implementation without reusing old baselines. Override the sampling settings with `BENCH_COUNT=12 BENCH_TIME=1s`. [Raw samples and environment/source metadata](bench/results/) are retained alongside the [median, observed range and allocation tables](bench/benchstats.txt). Some samples have substantial timing variation; ranges are reported rather than discarded and are not confidence intervals. Results are specific to this machine and toolchain.
