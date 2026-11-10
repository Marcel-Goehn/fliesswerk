// Package memory is what the Factory knows about memory: the estimate of
// what one Sandbox needs at its peak, kept in the runtime state per checkout
// and machine, whether another Sandbox fits, and how much the host has free.
package memory

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const (
	// DefaultEstimate is what one Sandbox is taken to need until one has been
	// measured.
	DefaultEstimate int64 = 2 << 30
	// Margin is kept free on top of the estimate.
	Margin int64 = 512 << 20
)

// raiseNumerator and raiseDenominator raise an estimate after an OOM kill, by
// half: what was measured wasn't enough.
const (
	raiseNumerator   = 3
	raiseDenominator = 2
)

// state is the file's content.
type state struct {
	// PeakBytes is the highest peak measured, 0 until one is.
	PeakBytes int64 `json:"peak_bytes"`
	// LastRunWaits is how often the last Run's Sandboxes waited for memory.
	LastRunWaits int `json:"last_run_waits"`
}

// Store is the estimate, on disk. It is safe for concurrent use.
type Store struct {
	path string
	mu   sync.Mutex
	s    state
}

// Load reads the store at path. Without a file it is empty; with an unreadable
// one it is empty too, and the error says why.
func Load(path string) (*Store, error) {
	st := &Store{path: path}
	data, err := os.ReadFile(path) //nolint:gosec // the Factory's own runtime state
	if errors.Is(err, os.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return st, fmt.Errorf("reading the memory estimate: %w", err)
	}
	if err := json.Unmarshal(data, &st.s); err != nil {
		st.s = state{}
		return st, fmt.Errorf("reading the memory estimate %s: %w", path, err)
	}
	return st, nil
}

// Estimate is what one Sandbox is taken to need at its peak.
func (st *Store) Estimate() int64 {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.estimate()
}

func (st *Store) estimate() int64 {
	if st.s.PeakBytes > 0 {
		return st.s.PeakBytes
	}
	return DefaultEstimate
}

// Measured reports whether the estimate comes from a measurement.
func (st *Store) Measured() bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.s.PeakBytes > 0
}

// LastRunWaits is how often the last Run's Sandboxes waited for memory.
func (st *Store) LastRunWaits() int {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.s.LastRunWaits
}

// Observe notes a measured peak. The first one replaces the default, and from
// then on only a higher one counts.
func (st *Store) Observe(peak int64) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	if peak <= st.s.PeakBytes {
		return nil
	}
	st.s.PeakBytes = peak
	return st.save()
}

// Raise raises the estimate after an OOM kill of a Sandbox that had reached
// seen, and returns the new one.
func (st *Store) Raise(seen int64) (int64, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.s.PeakBytes = max(st.estimate(), seen) * raiseNumerator / raiseDenominator
	return st.s.PeakBytes, st.save()
}

// SetLastRunWaits notes how often this Run's Sandboxes waited for memory.
func (st *Store) SetLastRunWaits(n int) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.s.LastRunWaits = n
	return st.save()
}

// save replaces the file, so that a crash can't leave half of one.
func (st *Store) save() error {
	data, err := json.Marshal(st.s)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(st.path), 0o750); err != nil {
		return err
	}
	tmp := st.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, st.path)
}

// Fits reports whether a Sandbox fits: free, less what is reserved for the
// Sandboxes already running, must hold the estimate and the margin. self is
// what the Sandbox already holds, 0 for one yet to start.
func Fits(free, reserved, estimate, self int64) bool {
	return free-reserved >= Needed(estimate, self)
}

// Needed is what a Sandbox holding self still needs free.
func Needed(estimate, self int64) int64 {
	return estimate + Margin - self
}

// Reserved is what a running Sandbox that holds used may still grow by.
func Reserved(estimate, used int64) int64 {
	return max(0, estimate-used)
}

// MB is bytes in whole megabytes, for people.
func MB(bytes int64) int64 { return bytes >> 20 }
