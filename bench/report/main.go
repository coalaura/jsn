package main

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"
)

type measurement struct {
	times  []float64
	bytes  float64
	allocs float64
}

var labels = []string{"encoding/json", "encoding/json/v2", "jsn dynamic", "jsn typed"}

func main() {
	err := run(os.Stdout, os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(output io.Writer, paths []string) error {
	if len(paths) != len(labels) {
		return fmt.Errorf("usage: report json_v1.txt json_v2.txt jsn.txt jsn_typed.txt")
	}

	results := make([]map[string]*measurement, len(paths))

	var (
		names   []string
		samples int
	)

	for index, path := range paths {
		file, err := os.Open(path)
		if err != nil {
			return err
		}

		results[index], err = parse(file)
		file.Close()

		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}

		if index == 0 {
			names = make([]string, 0, len(results[index]))

			for name := range results[index] {
				names = append(names, name)
			}

			slices.Sort(names)
		}

		if len(results[index]) != len(names) || len(names) == 0 {
			return fmt.Errorf("%s: missing or mismatched workloads", path)
		}

		for _, name := range names {
			entry, ok := results[index][name]
			if !ok || len(entry.times) < 2 {
				return fmt.Errorf("%s: need at least two samples for %s", path, name)
			}

			if samples == 0 {
				samples = len(entry.times)
			}

			if len(entry.times) != samples {
				return fmt.Errorf("%s: inconsistent sample count for %s", path, name)
			}

			slices.Sort(entry.times)
		}
	}

	fmt.Fprintf(output, "Median ns/op from %d samples per cell. Ranges are observed min-max, not confidence intervals.\n\n", samples)
	writeHeader(output)

	for _, name := range names {
		fmt.Fprintf(output, "| %s |", name)

		for _, result := range results {
			times := result[name].times
			middle := len(times) / 2
			median := times[middle]

			if len(times)%2 == 0 {
				median = (times[middle-1] + median) / 2
			}

			fmt.Fprintf(output, " %.2f |", median)
		}

		fmt.Fprintln(output)
	}

	fmt.Fprintln(output, "\nObserved ns/op ranges:")
	writeHeader(output)

	for _, name := range names {
		fmt.Fprintf(output, "| %s |", name)

		for _, result := range results {
			times := result[name].times
			fmt.Fprintf(output, " %.2f-%.2f |", times[0], times[len(times)-1])
		}

		fmt.Fprintln(output)
	}

	fmt.Fprintln(output, "\nMaximum B/op / allocs/op across samples:")
	writeHeader(output)

	for _, name := range names {
		fmt.Fprintf(output, "| %s |", name)

		for _, result := range results {
			entry := result[name]
			fmt.Fprintf(output, " %.0f / %.0f |", entry.bytes, entry.allocs)
		}

		fmt.Fprintln(output)
	}

	return nil
}

func parse(input io.Reader) (map[string]*measurement, error) {
	results := make(map[string]*measurement, 7)
	scanner := bufio.NewScanner(input)

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "BenchmarkEncode_") {
			continue
		}

		fields := strings.Fields(line)
		name, _, ok := strings.Cut(strings.TrimPrefix(fields[0], "BenchmarkEncode_"), "_")

		if !ok || len(fields) < 8 || len(fields)%2 != 0 {
			return nil, fmt.Errorf("malformed benchmark line: %s", line)
		}

		metrics := make(map[string]float64, 4)

		for index := 2; index < len(fields); index += 2 {
			value, err := strconv.ParseFloat(fields[index], 64)
			if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
				return nil, fmt.Errorf("invalid metric: %s", line)
			}

			metrics[fields[index+1]] = value
		}

		duration, hasTime := metrics["ns/op"]
		bytes, hasBytes := metrics["B/op"]
		allocs, hasAllocs := metrics["allocs/op"]

		if !hasTime || !hasBytes || !hasAllocs || duration == 0 {
			return nil, fmt.Errorf("missing benchmark metrics: %s", line)
		}

		entry := results[name]
		if entry == nil {
			entry = &measurement{times: make([]float64, 0, 12)}
			results[name] = entry
		}

		entry.times = append(entry.times, duration)
		entry.bytes = max(entry.bytes, bytes)
		entry.allocs = max(entry.allocs, allocs)
	}

	return results, scanner.Err()
}

func writeHeader(output io.Writer) {
	fmt.Fprint(output, "| Payload |")

	for _, label := range labels {
		fmt.Fprintf(output, " %s |", label)
	}

	fmt.Fprintln(output, "\n| :--- | ---: | ---: | ---: | ---: |")
}
