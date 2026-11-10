//go:build fwkfake

package main

import "time"

// In a test build, every retry waits only a moment, so
// end-to-end tests can run through every retry.
func init() {
	transientBackoff = func(int) time.Duration { return 10 * time.Millisecond }
	writeBackoff = func(int) time.Duration { return time.Millisecond }
}
