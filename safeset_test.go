package jsn

import "testing"

func TestSafeSet(t *testing.T) {
	var want [256]byte

	for i := 0x20; i < 256; i++ {
		want[i] = 1
	}

	want['\\'] = 0
	want['"'] = 0
	want['<'] = 0
	want['>'] = 0
	want['&'] = 0
	want[0xE2] = 0

	if safeSet == want {
		return
	}

	for i := range 256 {
		if safeSet[i] != want[i] {
			t.Errorf("safeSet[0x%02X]=%d, want %d", i, safeSet[i], want[i])
		}
	}
}
