//go:build !windows

package platform

// DecodeProcessOutput returns captured child-process output unchanged on
// non-Windows platforms, where UTF-8 is the expected process encoding.
func DecodeProcessOutput(data []byte) string {
	return string(data)
}
