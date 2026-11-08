//go:build !linux

package native

// RunHelper has nothing to run where there is no native Sandbox.
func RunHelper([]string) (int, bool) { return 0, false }
