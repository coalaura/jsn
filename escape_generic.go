//go:build !amd64

package jsn

func simdFirstEscape(s string) int {
	for i := 0; i < len(s); i++ {
		b := s[i]

		if safeSet[b] == 0 {
			return i
		}
	}

	return len(s)
}

func simdCopySafe(dst []byte, src string) int {
	for i := 0; i < len(src); i++ {
		b := src[i]

		if safeSet[b] == 0 {
			return i
		}

		dst[i] = b
	}

	return len(src)
}
