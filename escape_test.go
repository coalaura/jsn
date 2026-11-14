package jsn

import (
	"strings"
	"testing"
)

func TestEscapeHelperBoundaries(t *testing.T) {
	lengths := []int{0, 1, 7, 8, 15, 16, 17, 31, 32, 33, 47, 48, 49, 63, 64, 65, 127, 128, 129, 255, 256, 257}
	values := []byte{0, 0x1f, '"', '\\', '<', '>', '&', 0x7f, 0x80, 0xc2, 0xe2, 0xff}
	storage := make([]byte, 257+32)

	for _, length := range lengths {
		for alignment := range 16 {
			input := []byte(strings.Repeat("a", alignment+length))
			checkEscapeHelpers(t, string(input)[alignment:], storage, 15-alignment)

			for position := range length {
				for _, value := range values {
					input[alignment+position] = value
					checkEscapeHelpers(t, string(input)[alignment:], storage, 15-alignment)
				}

				input[alignment+position] = 'a'
			}
		}
	}
}

func TestEscapeHelperByteValues(t *testing.T) {
	storage := make([]byte, 64)

	for alignment := range 16 {
		input := []byte(strings.Repeat("a", alignment+32))

		for position := range 32 {
			for value := range 256 {
				input[alignment+position] = byte(value)
				checkEscapeHelpers(t, string(input)[alignment:], storage, alignment)
			}

			input[alignment+position] = 'a'
		}
	}
}

func TestEscapeHelpersDoNotAllocate(t *testing.T) {
	source := strings.Repeat("a", 257)
	destination := make([]byte, len(source))

	allocations := testing.AllocsPerRun(100, func() {
		simdFirstEscape(source)
		simdCopySafe(destination, source)
	})

	if allocations != 0 {
		t.Fatalf("escape helpers allocated %g times per run", allocations)
	}
}

func FuzzEscapeHelpers(f *testing.F) {
	f.Add("")
	f.Add("plain ASCII")
	f.Add("\x00\"\\<>&\x7f\x80\xff")
	f.Add(strings.Repeat("a", 15) + "\"" + strings.Repeat("b", 16) + "\n")
	f.Add(strings.Repeat("a", 256) + "\xe2\x80\xa8")

	f.Fuzz(func(t *testing.T, source string) {
		storage := make([]byte, len(source)+32)
		checkEscapeHelpers(t, source, storage, 7)
	})
}

func checkEscapeHelpers(t *testing.T, source string, storage []byte, offset int) {
	t.Helper()

	want := len(source)

	for index := range len(source) {
		value := source[index]
		if value < 0x20 || value >= 0x80 || value == '"' || value == '\\' || value == '<' || value == '>' || value == '&' {
			want = index

			break
		}
	}

	got := simdFirstEscape(source)
	if got != want {
		t.Fatalf("scan %q: got %d, want %d", source, got, want)
	}

	for index := range storage {
		storage[index] = 0xa5
	}

	// Bytes after the safe prefix may be copied, but no store may cross the
	// supplied destination bounds, even when spare backing storage exists.
	end := offset + len(source)
	destination := storage[offset:end:end]

	got = simdCopySafe(destination, source)
	if got != want {
		t.Fatalf("copy %q: got %d, want %d", source, got, want)
	}

	if string(destination[:got]) != source[:want] {
		t.Fatalf("copied prefix for %q: got %q, want %q", source, destination[:got], source[:want])
	}

	for index, value := range storage {
		if (index < offset || index >= end) && value != 0xa5 {
			t.Fatalf("copy %q changed guard byte %d outside [%d, %d)", source, index, offset, end)
		}
	}
}
