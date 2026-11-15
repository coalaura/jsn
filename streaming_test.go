package jsn

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

type observedWriter struct {
	bytes.Buffer
	maximum int
	failure error
	short   bool
}

type streamedJSON string

func (writer *observedWriter) Write(data []byte) (int, error) {
	writer.maximum = max(writer.maximum, len(data))
	if writer.failure != nil {
		return 0, writer.failure
	}

	if writer.short {
		return len(data) - 1, nil
	}

	return writer.Buffer.Write(data)
}

func (value streamedJSON) MarshalJSONTo(writer io.Writer) error {
	_, err := writer.Write([]byte(value))

	return err
}

func TestLargeScalarStreaming(t *testing.T) {
	text := strings.Repeat("<large>&", 32768)

	raw, err := json.Marshal(text)
	if err != nil {
		t.Fatal(err)
	}

	values := []any{text, []byte(text), json.RawMessage(raw), outputJSON(raw), streamedJSON(raw)}

	for _, value := range values {
		writer := new(observedWriter)
		encoder := NewEncoder(writer)

		err := encoder.Encode(value)
		if err != nil {
			t.Fatal(err)
		}

		want := raw

		if _, ok := value.(streamedJSON); !ok {
			want, err = json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
		}

		if !bytes.Equal(bytes.TrimSpace(writer.Bytes()), want) || writer.maximum <= 4096 || cap(encoder.buf) < len(want) {
			t.Fatalf("%T: output differs or large scalar was not buffered", value)
		}

		capacity := cap(encoder.buf)
		writer.Reset()

		err = encoder.Encode(true)
		if err != nil || cap(encoder.buf) != capacity {
			t.Fatalf("%T: retained capacity changed: %v", value, err)
		}
	}
}

func TestEncoderWriterErrors(t *testing.T) {
	failure := errors.New("writer failure")
	large := strings.Repeat("x", 16384)
	values := []any{large, []byte(large), json.RawMessage(`"` + large + `"`), streamedJSON(`"` + large + `"`), []string{large, large}}
	conditions := []bool{false, true}

	for _, short := range conditions {
		for _, value := range values {
			writer := &observedWriter{failure: failure, short: short}
			want := failure

			if short {
				writer.failure = nil
				want = io.ErrShortWrite
			}

			err := NewEncoder(writer).Encode(value)
			if !errors.Is(err, want) {
				t.Errorf("%T: got %v, want %v", value, err, want)
			}
		}
	}
}
