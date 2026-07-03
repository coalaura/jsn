//go:build amd64

package jsn

//go:noescape
func simdFirstEscape(s string) int

//go:noescape
func simdCopySafe(dst []byte, src string) int
