# jsn

`jsn` is a high-performance Go JSON encoder and decoder. It compiles cached, type-specific plans to reduce runtime reflection and interface boxing on hot paths. The encoder supports zero-allocation steady-state operation; the decoder owns its output strings and allocates storage as needed.

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

### Decoding

Use `jsn.Unmarshal(data, &value)` for complete JSON buffers or compile a typed decoder once for repeated use:

```go
var userDecoder = jsn.CompileTypedDecoder[User]()

func decodeUser(data []byte) (User, error) {
	var user User

	err := userDecoder.Unmarshal(data, &user)

	return user, err
}
```

`jsn.NewDecoder(reader)` supports `Decode`, `UseNumber`, `DisallowUnknownFields`, `Buffered`, `InputOffset`, `More` and `Token`. Typed streaming uses `userDecoder.Decode(decoder, &user)`. Stream framing and tokens currently use `encoding/json.Decoder`; the compiled fast path targets complete-buffer decoding.

Decoding follows `encoding/json` conventions for struct tags, case-insensitive field matching, duplicate keys, nulls, destination reuse and custom JSON/text unmarshaling. Errors and tokens use the standard library's types. Decoded strings, map keys and raw messages do not borrow the input buffer. Compiled plans are safe to share across goroutines with separate destinations; stream decoders are not.

Unlike `encoding/json.Unmarshal`, complete-buffer decoding validates in a single pass, so malformed JSON can leave the destination partially populated and invoke custom unmarshaling before a later syntax error is found. Validate with `json.Valid` first if syntax-error atomicity is required.

## Notes

- **Map key ordering is non-deterministic.** Unlike `encoding/json` which sorts map keys, `jsn` emits them in Go's runtime map iteration order. This is not a bug for 99% of use cases (JSON objects are unordered by spec), but byte-for-byte output comparison across runs will fail. If you need stable output, sort keys before encoding.

## Performance Benchmarks

### Encoding

Median time per encode, measured on 2026-11-14 with Go 1.27.4, Windows/amd64 (`GOAMD64=v1`, default `GOEXPERIMENT`) and an AMD Ryzen 9 9950X3D. Each cell contains 12 one-second samples from the same compiled benchmark binary, with library execution order rotated each round. Every sample performs 100 untimed warm-up encodes before measurement.

| Payload | `encoding/json` | `encoding/json/v2` | jsn `Encoder.Encode` | jsn `TypedEncoder.Encode` |
| :--- | ---: | ---: | ---: | ---: |
| AllTypes | 785.30 ns | 672.80 ns | 187.30 ns | 173.50 ns |
| Small | 211.45 ns | 200.05 ns | 90.83 ns | 75.31 ns |
| Medium | 927.90 ns | 789.05 ns | 225.20 ns | 206.70 ns |
| Large | 204.63 us | 175.62 us | 48.31 us | 48.06 us |
| Deep | 2.36 us | 2.52 us | 992.55 ns (0.99us) | 778.80 ns (0.78us) |
| Map | 19.40 us | 8.02 us | 2.50 us | 2.64 us |
| Document | 43.05 us | 43.80 us | 22.56 us | 22.36 us |

Both jsn APIs reported **0 B/op and 0 allocs/op in every sample**. These are warmed, steady-state measurements into `io.Discard`, reusing heap-resident input values and encoder buffers. Encoder construction, type compilation and fixture validation are outside the timed loops; these numbers do not measure first-use cost or fresh-input allocations.

All implementations enable HTML and JavaScript escaping. Each benchmark validates UTF-8, JSON content, output length and newline framing before timing and checks errors during warm-up and measurement. `encoding/json` sorts map keys; jsn and the configured v2 encoder do not, so map-containing rows include that additional work for `encoding/json`.

Run the comparison from the repository root with Go 1.27 or later:

```bash
bash bench/bench.sh
```

The runner tests the suite, rebuilds once and reruns every implementation without reusing old baselines. Override the sampling settings with `BENCH_COUNT=12 BENCH_TIME=1s`. [Raw samples and environment/source metadata](bench/results/) are retained alongside the [median, observed range and allocation tables](bench/benchstats.txt). Some samples have substantial timing variation; ranges are reported rather than discarded and are not confidence intervals. Results are specific to this machine and toolchain.

### Decoding

Median time per complete-buffer decode, measured on the same date, machine and toolchain as encoding. Each cell contains three 300 ms samples, with library execution order rotated each round and 100 untimed warm-up decodes before each sample. Standard v1 uses `encoding/json.Unmarshal`; standard v2 uses `encoding/json/v2.Unmarshal` with default options.

| Payload | Destination | `encoding/json` | `encoding/json/v2` | jsn `Unmarshal` | jsn `TypedDecoder.Unmarshal` |
| :--- | :--- | ---: | ---: | ---: | ---: |
| AllTypes | Reused | 1.11 us | 1.14 us | 426.0 ns (0.43us) | 378.8 ns (0.38us) |
| AllTypes | Fresh | 1.27 us | 1.03 us | 574.6 ns (0.57us) | 523.2 ns (0.52us) |
| Small | Reused | 512.6 ns | 415.8 ns | 283.4 ns | 243.2 ns |
| Small | Fresh | 618.9 ns | 503.7 ns | 330.8 ns | 287.9 ns |
| Medium | Reused | 1.46 us | 1.43 us | 686.9 ns (0.69us) | 660.6 ns (0.66us) |
| Medium | Fresh | 1.76 us | 1.49 us | 905.3 ns (0.91us) | 908.6 ns (0.91us) |
| Large | Reused | 295.97 us | 334.15 us | 142.69 us | 145.38 us |
| Large | Fresh | 382.35 us | 362.70 us | 209.40 us | 226.68 us |
| Deep | Reused | 4.21 us | 4.07 us | 2.10 us | 2.15 us |
| Deep | Fresh | 4.96 us | 4.32 us | 3.23 us | 3.20 us |
| Map | Reused | 16.40 us | 32.16 us | 6.31 us | 6.33 us |
| Map | Fresh | 19.93 us | 22.34 us | 12.45 us | 14.03 us |
| Document | Reused | 126.09 us | 117.55 us | 77.90 us | 84.32 us |
| Document | Fresh | 165.28 us | 141.90 us | 103.20 us | 105.52 us |

Reused destinations retain their storage between iterations; fresh destinations are allocated inside each timed iteration. Both modes use warmed type plans and owned output strings. Decoding can allocate in either mode. Type compilation, input preparation and decoded-value validation are outside the timed loops; every implementation is validated before and after timing. These measurements exclude streaming and first-use costs.

Run the decoder comparison from `bench/` with Go 1.27 or later:

```bash
go test -run '^$' -bench '^BenchmarkDecode$' -benchmem
```
