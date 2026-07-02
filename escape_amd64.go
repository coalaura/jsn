//go:build amd64

package jsn

//go:noescape
func simdFirstEscape(s string) int
