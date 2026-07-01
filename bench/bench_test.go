package jsn_test

import (
	"encoding/json"
	"io"
	"strconv"
	"testing"

	"github.com/coalaura/jsn"
)

type Small struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	IsBot bool   `json:"is_bot"`
}

type Medium struct {
	User     Small            `json:"user"`
	Roles    []string         `json:"roles"`
	Settings map[string]int64 `json:"settings"`
}

type Node struct {
	Name     string  `json:"name"`
	Value    int64   `json:"value"`
	Children []*Node `json:"children,omitempty"`
}

type Large struct {
	Users []Medium          `json:"users"`
	Nodes *Node             `json:"nodes"`
	Extra map[string]string `json:"extra"`
}

var (
	smallData *Small
	medData   *Medium
	largeData *Large
	deepData  *Node
	mapData   map[string]int64

	smallEnc = jsn.Compile[*Small]()
	medEnc   = jsn.Compile[*Medium]()
	largeEnc = jsn.Compile[*Large]()
	deepEnc  = jsn.Compile[*Node]()
)

func init() {
	smallData = &Small{ID: 102930192, Name: "Mechanically Sympathetic User", IsBot: false}
	medData = &Medium{
		User:     *smallData,
		Roles:    []string{"admin", "user", "guest"},
		Settings: map[string]int64{"theme": 1, "notifications": 0},
	}

	// Init Depth 10 Tree
	deepData = &Node{Name: "Root", Value: 1}
	curr := deepData
	for i := range 10 {
		child := &Node{Name: "Child", Value: int64(i)}
		curr.Children = []*Node{child}
		curr = child
	}

	// Init Large Slice of structs
	users := make([]Medium, 100)
	for i := range 100 {
		users[i] = *medData
	}
	extra := make(map[string]string)
	for i := range 100 {
		extra[strconv.Itoa(i)] = "value"
	}
	largeData = &Large{
		Users: users,
		Nodes: deepData,
		Extra: extra,
	}

	// Init Map Data
	mapData = make(map[string]int64)
	for i := range 100 {
		mapData[strconv.Itoa(i)] = int64(i)
	}
}

func BenchmarkEncode_Small_Std(b *testing.B) {
	enc := json.NewEncoder(io.Discard)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = enc.Encode(smallData)
	}
}

func BenchmarkEncode_Small_Jsn(b *testing.B) {
	enc := jsn.NewEncoder(io.Discard)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = enc.EncodeAs(smallEnc, smallData)
	}
}

func BenchmarkEncode_Medium_Std(b *testing.B) {
	enc := json.NewEncoder(io.Discard)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = enc.Encode(medData)
	}
}

func BenchmarkEncode_Medium_Jsn(b *testing.B) {
	enc := jsn.NewEncoder(io.Discard)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = enc.EncodeAs(medEnc, medData)
	}
}

func BenchmarkEncode_Large_Std(b *testing.B) {
	enc := json.NewEncoder(io.Discard)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = enc.Encode(largeData)
	}
}

func BenchmarkEncode_Large_Jsn(b *testing.B) {
	enc := jsn.NewEncoder(io.Discard)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = enc.EncodeAs(largeEnc, largeData)
	}
}

func BenchmarkEncode_Deep_Std(b *testing.B) {
	enc := json.NewEncoder(io.Discard)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = enc.Encode(deepData)
	}
}

func BenchmarkEncode_Deep_Jsn(b *testing.B) {
	enc := jsn.NewEncoder(io.Discard)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = enc.EncodeAs(deepEnc, deepData)
	}
}

func BenchmarkEncode_Map_Std(b *testing.B) {
	enc := json.NewEncoder(io.Discard)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = enc.Encode(mapData)
	}
}

func BenchmarkEncode_Map_Jsn(b *testing.B) {
	enc := jsn.NewEncoder(io.Discard)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = enc.Encode(mapData)
	}
}
