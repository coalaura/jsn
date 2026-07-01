# gnuplot <benchmark.gnuplot

reset

# for windows
set encoding utf8

set terminal pngcairo font "simsun,12" size 1200,850 noenhanced

set style data linespoints
set pointsize 0.8

set output "benchmark.png"
set title "JSON Benchmark (Low is Better)"
set xlabel ""
set ylabel "ns/op"
set xtics rotate by -90

set key right top
set key spacing 1.2
set grid ytics

#set yrange [0:50]
plot \
	"benchmark_result_jsn.txt" using 3:xticlabels(1) title "coalaura/jsn" with linespoints, \
	"benchmark_result_std.txt" using 3:xticlabels(1) title "encoding/json" with linespoints
