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

type Author struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	Bio   string `json:"bio"`
}

type Comment struct {
	ID      int64  `json:"id"`
	Author  Author `json:"author"`
	Body    string `json:"body"`
	Upvotes int64  `json:"upvotes"`
}

type DocumentStats struct {
	Views    int64   `json:"views"`
	Likes    int64   `json:"likes"`
	Shares   int64   `json:"shares"`
	Rating   float64 `json:"rating"`
	Featured bool    `json:"featured"`
}

type Attachment struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	Content []byte `json:"content"`
}

type PtrByteMarshaler struct {
	Value string `json:"value"`
}

func (p *PtrByteMarshaler) MarshalJSON() ([]byte, error) {
	return []byte(`{"custom":"` + p.Value + `"}`), nil
}

type Embedded struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type WithEmbed struct {
	Embedded
	Extra string `json:"extra"`
}

type WithRaw struct {
	ID  int             `json:"id"`
	Raw json.RawMessage `json:"raw"`
}

type Document struct {
	ID          int64          `json:"id"`
	Title       string         `json:"title"`
	Slug        string         `json:"slug"`
	Body        string         `json:"body"`
	Tags        []string       `json:"tags"`
	Author      Author         `json:"author"`
	Stats       DocumentStats  `json:"stats"`
	Metadata    map[string]any `json:"metadata"`
	Comments    []Comment      `json:"comments"`
	Attachments []Attachment   `json:"attachments"`
	Published   time.Time      `json:"published"`
	Modified    time.Time      `json:"modified"`
}

var (
	smallData        *Small
	allTypesData     *AllTypes
	medData          *Medium
	largeData        *Large
	deepData         *Node
	mapData          map[string]int64
	docData          *Document
	ptrMarshalerData *PtrByteMarshaler
	embedData        *WithEmbed
	rawData          *WithRaw

	smallEnc        = jsn.CompileTyped[Small]()
	allTypesEnc     = jsn.CompileTyped[AllTypes]()
	medEnc          = jsn.CompileTyped[Medium]()
	largeEnc        = jsn.CompileTyped[Large]()
	deepEnc         = jsn.CompileTyped[Node]()
	docEnc          = jsn.CompileTyped[Document]()
	ptrMarshalerEnc = jsn.CompileTyped[PtrByteMarshaler]()
	embedEnc        = jsn.CompileTyped[WithEmbed]()
	rawEnc          = jsn.CompileTyped[WithRaw]()
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

	paragraphs := []string{
		// ascii
		"The quick brown fox jumps over the lazy dog near the riverbank on a sunny afternoon while children play nearby.",

		// quotes and backslashes
		"The system reported \"critical\" status at C:\\Program Files\\app\\logs\\error.log when the process exited with code 42.",

		// unicode
		"Unicode characters include \u2605 star \u00e9 cafe \u00fc uber \u4e2d\u6587 Chinese \u65e5\u672c\u8a9e Japanese \U0001F600 emoji here.",

		// json-special unicode (U+2028/U+2029)
		"Line separator \u2028 and paragraph separator \u2029 must be escaped in JSON output to avoid breaking JavaScript parsers.",

		// html-like
		"HTML content: <div class=\"widget\">&amp;</div> with <script>alert('xss')</script> and <img src='x' onerror='evil()'> tags.",

		// regex patterns
		"Regex patterns like \\d{3}-\\d{4} for phone numbers, \\w+@\\w+\\.\\w+ for emails, \\bword\\b for word boundaries in text.",

		// dense special characters
		"Mixed special chars: @#$%^&*(){}[]|;:'\",.<>?/\\~` plus \"quotes\" and 'apostrophes' and back\\slashes everywhere.",

		// control characters
		"Control characters: tab\there, newline\nhere, carriage\rreturn, and even a null\x00byte need proper JSON escaping.",

		// numbers in text
		"Numeric values like 42, 3.14159, -273.15, 1e10, 6.022e23, 0xDEADBEEF appear naturally in technical documentation and logs.",

		// long pure ascii
		"This particular paragraph has absolutely no characters that need escaping whatsoever so the SIMD scanner should blaze through it without finding any matches and return the full length immediately without entering the slow path at all which is the best case scenario for throughput testing in our JSON encoder benchmark suite.",
	}

	var body string

	for i := range 300 {
		if i > 0 {
			body += "\n\n"
		}

		body += paragraphs[i%len(paragraphs)]
	}

	comments := make([]Comment, 50)

	for i := range 50 {
		comments[i] = Comment{
			ID: int64(i + 1),
			Author: Author{
				Name:  "user_" + strconv.Itoa(i),
				Email: "user" + strconv.Itoa(i) + "@example.com",
				Bio:   "A \"brief\" bio with \\backslashes\\ and \n newlines.",
			},
			Body:    paragraphs[i%len(paragraphs)],
			Upvotes: int64(i * 7 % 1000),
		}
	}

	pngContent := make([]byte, 256)

	for i := range pngContent {
		pngContent[i] = byte(i)
	}

	binContent := make([]byte, 512)

	for i := range binContent {
		binContent[i] = byte(i * 7)
	}

	docData = &Document{
		ID:    48213,
		Title: "Deep Dive: \"High-Performance\" JSON Encoding in Go",
		Slug:  "deep-dive-json-encoding-go",
		Body:  body,
		Tags:  []string{"go", "json", "performance", "simd", "assembly"},
		Author: Author{
			Name:  "Jane \"Code\" Doe",
			Email: "jane@example.com",
			Bio:   "Systems engineer specializing in \\low-level\\ optimization and \U0001F680 performance.",
		},
		Stats: DocumentStats{
			Views:    48213,
			Likes:    1294,
			Shares:   87,
			Rating:   4.7,
			Featured: true,
		},
		Metadata: map[string]any{
			"version":      "1.2.3",
			"word_count":   int64(48213),
			"read_minutes": int64(15),
			"featured":     true,
			"rating":       4.7,
			"categories":   []string{"engineering", "go", "performance"},
			"license":      "MIT",
		},
		Comments: comments,
		Attachments: []Attachment{
			{Name: "diagram.png", Type: "image/png", Content: pngContent},
			{Name: "data.bin", Type: "application/octet-stream", Content: binContent},
		},
		Published: time.Date(2026, 7, 2, 1, 2, 21, 0, time.UTC),
		Modified:  time.Date(2026, 7, 2, 5, 45, 41, 0, time.UTC),
	}

	ptrMarshalerData = &PtrByteMarshaler{Value: "hello"}
	embedData = &WithEmbed{Embedded: Embedded{ID: 1, Name: "test"}, Extra: "data"}
	rawData = &WithRaw{ID: 1, Raw: json.RawMessage(`{"nested":true,"val":42}`)}
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

func BenchmarkEncode_Document_Std(b *testing.B) {
	enc := json.NewEncoder(io.Discard)

	for range 100 {
		_ = enc.Encode(docData)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		_ = enc.Encode(docData)
	}
}

func BenchmarkEncode_Document_Std2(b *testing.B) {
	enc := jsontext.NewEncoder(io.Discard)

	for range 100 {
		_ = json2.MarshalEncode(enc, docData)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		_ = json2.MarshalEncode(enc, docData)
	}
}

func BenchmarkEncode_Document_Jsn(b *testing.B) {
	enc := jsn.NewEncoder(io.Discard)

	for range 100 {
		_ = docEnc.Encode(enc, docData)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		_ = docEnc.Encode(enc, docData)
	}
}
