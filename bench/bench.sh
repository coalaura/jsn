#!/bin/bash
(
	set -euo pipefail

	mkdir -p results

	export GOEXPERIMENT=jsonv2

	pat() {
		printf '^BenchmarkEncode_(%s)_%s$' "$1" "$2"
	}

	FAST_TYPES='Small|AllTypes|Medium|Deep|Map'
	SLOW_TYPES='Large|Document'

	run() {
		local label="$1"
		local pattern="$2"
		local benchtime="$3"

		go test -bench="${pattern}" -benchtime="${benchtime}" -count=10 | awk '/^Benchmark/ { sub(/_(Std|Std2|Jsn)-/, "_"); print }' >> "results/${label}.txt"
	}

	if [ ! -f "results/json_v1.txt" ]; then
		echo "Benchmarking encoding/json..."

		run "json_v1" "$(pat "${FAST_TYPES}" Std)" "1000000x"
		run "json_v1" "$(pat "${SLOW_TYPES}" Std)" "3s"
	fi

	if [ ! -f "results/json_v2.txt" ]; then
		echo "Benchmarking encoding/json/v2..."

		run "json_v2" "$(pat "${FAST_TYPES}" Std2)" "1000000x"
		run "json_v2" "$(pat "${SLOW_TYPES}" Std2)" "3s"
	fi

	echo "Benchmarking coalaura/jsn..."

	rm -f results/jsn.txt

	run "jsn" "$(pat "${FAST_TYPES}" Jsn)" "1000000x"
	run "jsn" "$(pat "${SLOW_TYPES}" Jsn)" "3s"

	benchstat results/json_v1.txt results/json_v2.txt results/jsn.txt > benchstats.txt

	echo "Done"
)