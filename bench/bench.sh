#!/usr/bin/env bash
set -euo pipefail

cd -- "$(dirname -- "${BASH_SOURCE[0]}")"

COUNT="${BENCH_COUNT:-12}"
TIME="${BENCH_TIME:-1s}"

if [[ ! "$COUNT" =~ ^[1-9][0-9]*$ ]] || (( COUNT < 2 )); then
	printf 'BENCH_COUNT must be an integer >= 2\n' >&2
	exit 1
fi

if [[ ! "$TIME" =~ ^[0-9]+(\.[0-9]+)?(ns|us|ms|s|m)$ ]]; then
	printf 'BENCH_TIME must be a duration such as 500ms or 1s\n' >&2
	exit 1
fi

mkdir -p results
WORK="$(mktemp -d ./results/.run.XXXXXX)"
trap 'rm -rf -- "$WORK"' EXIT

go test ./...
go test -c -o "$WORK/bench.test.exe" .

{
	printf 'utc: %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
	printf 'samples: %s\nbenchtime: %s\nwarmup: 100 encodes per sample\n' "$COUNT" "$TIME"
	printf 'order: rotate Std, Std2, Jsn, JsnTyped each round\n'
	printf 'sink: io.Discard; reused encoder and input; compilation excluded\n'
	printf 'escaping: HTML and JavaScript enabled for all implementations\n'
	printf 'ordering: Std sorts map keys; Std2 and jsn do not\n'

	go version

	go env -json GOOS GOARCH GOAMD64 GOARM64 GOEXPERIMENT GOTOOLCHAIN GOFLAGS

	printf 'GOMAXPROCS: %s\nGODEBUG: %s\n' "${GOMAXPROCS:-default}" "${GODEBUG:-default}"
	printf 'commit: %s\n' "$(git rev-parse HEAD)"

	git status --short --untracked-files=normal -- ..

	printf 'source hashes (git blob SHA, path):\n'

	for source in ../*.go ../*.s ./*.go bench.sh go.mod report/*.go; do
		printf '%s %s\n' "$(git hash-object "$source")" "$source"
	done
} > "$WORK/environment.txt"

IMPLEMENTATIONS=(Std Std2 Jsn JsnTyped)
LABELS=(json_v1 json_v2 jsn jsn_typed)

for ((round = 0; round < COUNT; round++)); do
	for ((slot = 0; slot < 4; slot++)); do
		index=$(((round + slot) % 4))
		implementation="${IMPLEMENTATIONS[index]}"
		label="${LABELS[index]}"

		printf 'Round %d/%d: %s\n' "$((round + 1))" "$COUNT" "$implementation"

		"$WORK/bench.test.exe" -test.run='^$' -test.bench="^BenchmarkEncode_.*_${implementation}$" -test.benchmem -test.benchtime="$TIME" -test.count=1 | sed 's/[[:blank:]]*$//' >> "$WORK/${label}.txt"
	done
done

go run ./report "$WORK/json_v1.txt" "$WORK/json_v2.txt" "$WORK/jsn.txt" "$WORK/jsn_typed.txt" > "$WORK/benchstats.txt"

# Publish only a complete, validated run; failed runs leave previous results intact.
for label in "${LABELS[@]}"; do
	mv -- "$WORK/${label}.txt" "results/${label}.txt"
done

mv -- "$WORK/environment.txt" results/environment.txt
mv -- "$WORK/benchstats.txt" benchstats.txt

printf 'Done. Raw samples: bench/results/. Median and range tables: bench/benchstats.txt.\n'
