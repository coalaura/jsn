//go:build !amd64

package jsn

func simdFirstEscape(s string) int {
	for i := 0; i < len(s); i++ {
		if safeSet[s[i]] == 0 {
			return i
		}
	}
	return len(s)
}
