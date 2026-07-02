package jsn_test

import (
	"encoding/json"
	"encoding/json/jsontext"
	json2 "encoding/json/v2"
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

	smallEnc    = jsn.CompileTyped[Small]()
	allTypesEnc = jsn.CompileTyped[AllTypes]()
	medEnc      = jsn.CompileTyped[Medium]()
	largeEnc    = jsn.CompileTyped[Large]()
	deepEnc     = jsn.CompileTyped[Node]()
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

	for range 1000 {
		_ = enc.Encode(allTypesData)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		_ = enc.Encode(allTypesData)
	}
}

func BenchmarkEncode_AllTypes_Std2(b *testing.B) {
	enc := jsontext.NewEncoder(io.Discard)

	for range 1000 {
		_ = json2.MarshalEncode(enc, allTypesData)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		_ = json2.MarshalEncode(enc, allTypesData)
	}
}

func BenchmarkEncode_AllTypes_Jsn(b *testing.B) {
	enc := jsn.NewEncoder(io.Discard)

	for range 1000 {
		_ = allTypesEnc.Encode(enc, allTypesData)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		_ = allTypesEnc.Encode(enc, allTypesData)
	}
}

func BenchmarkEncode_Small_Std(b *testing.B) {
	enc := json.NewEncoder(io.Discard)

	for range 1000 {
		_ = enc.Encode(smallData)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		_ = enc.Encode(smallData)
	}
}

func BenchmarkEncode_Small_Std2(b *testing.B) {
	enc := jsontext.NewEncoder(io.Discard)

	for range 1000 {
		_ = json2.MarshalEncode(enc, smallData)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		_ = json2.MarshalEncode(enc, smallData)
	}
}

func BenchmarkEncode_Small_Jsn(b *testing.B) {
	enc := jsn.NewEncoder(io.Discard)

	for range 1000 {
		_ = smallEnc.Encode(enc, smallData)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		_ = smallEnc.Encode(enc, smallData)
	}
}

func BenchmarkEncode_Medium_Std(b *testing.B) {
	enc := json.NewEncoder(io.Discard)

	for range 500 {
		_ = enc.Encode(medData)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		_ = enc.Encode(medData)
	}
}

func BenchmarkEncode_Medium_Std2(b *testing.B) {
	enc := jsontext.NewEncoder(io.Discard)

	for range 500 {
		_ = json2.MarshalEncode(enc, medData)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		_ = json2.MarshalEncode(enc, medData)
	}
}

func BenchmarkEncode_Medium_Jsn(b *testing.B) {
	enc := jsn.NewEncoder(io.Discard)

	for range 500 {
		_ = medEnc.Encode(enc, medData)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		_ = medEnc.Encode(enc, medData)
	}
}

func BenchmarkEncode_Large_Std(b *testing.B) {
	enc := json.NewEncoder(io.Discard)

	for range 100 {
		_ = enc.Encode(largeData)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		_ = enc.Encode(largeData)
	}
}

func BenchmarkEncode_Large_Std2(b *testing.B) {
	enc := jsontext.NewEncoder(io.Discard)

	for range 100 {
		_ = json2.MarshalEncode(enc, largeData)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		_ = json2.MarshalEncode(enc, largeData)
	}
}

func BenchmarkEncode_Large_Jsn(b *testing.B) {
	enc := jsn.NewEncoder(io.Discard)

	for range 100 {
		_ = largeEnc.Encode(enc, largeData)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		_ = largeEnc.Encode(enc, largeData)
	}
}

func BenchmarkEncode_Deep_Std(b *testing.B) {
	enc := json.NewEncoder(io.Discard)

	for range 100 {
		_ = enc.Encode(deepData)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		_ = enc.Encode(deepData)
	}
}

func BenchmarkEncode_Deep_Std2(b *testing.B) {
	enc := jsontext.NewEncoder(io.Discard)

	for range 100 {
		_ = json2.MarshalEncode(enc, deepData)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		_ = json2.MarshalEncode(enc, deepData)
	}
}

func BenchmarkEncode_Deep_Jsn(b *testing.B) {
	enc := jsn.NewEncoder(io.Discard)

	for range 100 {
		_ = deepEnc.Encode(enc, deepData)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		_ = deepEnc.Encode(enc, deepData)
	}
}

func BenchmarkEncode_Map_Std(b *testing.B) {
	enc := json.NewEncoder(io.Discard)

	for range 500 {
		_ = enc.Encode(mapData)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		_ = enc.Encode(mapData)
	}
}

func BenchmarkEncode_Map_Std2(b *testing.B) {
	enc := jsontext.NewEncoder(io.Discard)

	for range 500 {
		_ = json2.MarshalEncode(enc, mapData)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		_ = json2.MarshalEncode(enc, mapData)
	}
}

func BenchmarkEncode_Map_Jsn(b *testing.B) {
	enc := jsn.NewEncoder(io.Discard)

	for range 500 {
		_ = enc.Encode(mapData)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		_ = enc.Encode(mapData)
	}
}
