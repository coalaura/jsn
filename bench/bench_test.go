package jsn_test

import (
	"encoding/json"
	"io"
	"strconv"
	"testing"
	"time"

	"github.com/coalaura/jsn"
)

type Small struct {
	ID     int64   `json:"id"`
	Name   string  `json:"name"`
	IsBot  bool    `json:"is_bot"`
	Score  float64 `json:"score"`
	Status *string `json:"status"` // Pointer type
}

type AllTypes struct {
	IntVal    int            `json:"int_val"`
	UintVal   uint64         `json:"uint_val"`
	FloatVal  float32        `json:"float_val"`
	StringVal string         `json:"string_val"`
	BoolVal   bool           `json:"bool_val"`
	SliceVal  []byte         `json:"slice_val"` // Base64 encoding test
	TimeVal   time.Time      `json:"time_val"`
	MapVal    map[string]any `json:"map_val"`
}

type Medium struct {
	User     Small            `json:"user"`
	Roles    []string         `json:"roles"`
	Settings map[string]int64 `json:"settings"`
	Flags    [4]bool          `json:"flags"` // Array type
	Created  time.Time        `json:"created"`
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
	smallData    *Small
	allTypesData *AllTypes
	medData      *Medium
	largeData    *Large
	deepData     *Node
	mapData      map[string]int64

	smallEnc    = jsn.Compile[*Small]()
	allTypesEnc = jsn.Compile[*AllTypes]()
	medEnc      = jsn.Compile[*Medium]()
	largeEnc    = jsn.Compile[*Large]()
	deepEnc     = jsn.Compile[*Node]()
)

func init() {
	status := "active_status"

	smallData = &Small{
		ID:     102930192,
		Name:   "Some random User \u2605 with \"escapes\" & \n newlines",
		IsBot:  false,
		Score:  99.987,
		Status: &status,
	}

	allTypesData = &AllTypes{
		IntVal:    -42,
		UintVal:   18446744073709551615,
		FloatVal:  3.14159,
		StringVal: "Standard and Custom Serialization",
		BoolVal:   true,
		SliceVal:  []byte{0xde, 0xad, 0xbe, 0xef},
		TimeVal:   time.Date(2026, 7, 2, 1, 2, 21, 0, time.UTC),
		MapVal:    map[string]any{"key1": "val1", "key2": 123.45},
	}

	medData = &Medium{
		User:     *smallData,
		Roles:    []string{"admin", "user", "guest", "moderator", "billing"},
		Settings: map[string]int64{"theme": 1, "notifications": 0, "frequency": 42},
		Flags:    [4]bool{true, false, true, true},
		Created:  time.Date(2026, 7, 2, 1, 2, 21, 0, time.UTC),
	}

	deepData = &Node{Name: "Root", Value: 1}

	curr := deepData

	for i := range 20 {
		child := &Node{Name: "Child", Value: int64(i)}

		curr.Children = []*Node{child}

		curr = child
	}

	users := make([]Medium, 200)

	for i := range 200 {
		users[i] = *medData
	}

	extra := make(map[string]string)

	for i := range 200 {
		extra[strconv.Itoa(i)] = "value_with_some_length_to_test_allocation"
	}

	largeData = &Large{
		Users: users,
		Nodes: deepData,
		Extra: extra,
	}

	mapData = make(map[string]int64)

	for i := range 200 {
		mapData[strconv.Itoa(i)] = int64(i)
	}
}

func BenchmarkEncode_AllTypes_Std(b *testing.B) {
	enc := json.NewEncoder(io.Discard)

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		_ = enc.Encode(allTypesData)
	}
}

func BenchmarkEncode_AllTypes_Jsn(b *testing.B) {
	enc := jsn.NewEncoder(io.Discard)

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		_ = enc.EncodeAs(allTypesEnc, allTypesData)
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
