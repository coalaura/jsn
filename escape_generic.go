//go:build !amd64

package jsn

func simdFirstEscape(s string) int {
	for i := 0; i < len(s); i++ {
		b := s[i]

		if b == 0xE2 {
			// only stop for U+2028/2029 prefix (0xE2 0x80)
			if i+1 < len(s) && s[i+1] == 0x80 {
				return i
			}

			continue
		}

		if safeSet[b] == 0 {
			return i
		}
	}

	return len(s)
}
