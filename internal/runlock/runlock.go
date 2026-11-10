// Package runlock keeps a second Run out of a checkout. The lock is a file
// in the runtime state that holds the PID of the Run that took it. A Run
// that ended without releasing it (a crash, a kill) leaves it stale: its
// PID is no longer running, and the next Run takes it over.
package runlock

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// HeldError says a live Run holds the lock.
type HeldError struct {
	PID  int
	Path string
}

func (e *HeldError) Error() string {
	return fmt.Sprintf("another Run holds this checkout's lock (PID %d); wait for it to end, or stop it. If PID %d isn't a Run (a crashed Run's PID can be reused), delete %s",
		e.PID, e.PID, e.Path)
}

// Lock is a lock this process holds.
type Lock struct {
	path string
	// StalePID is the PID of the dead Run whose lock was taken over, 0 if
	// there was none.
	StalePID int
}

// How long Acquire waits for a lock that was only just created to get its
// PID, before it counts as left by a Run that died in that moment.
const (
	writeWait  = 50 * time.Millisecond
	writeTries = 10
)

// Acquire takes the lock at path, or returns a *HeldError naming the Run
// that holds it. Two Runs starting in the same instant over a stale lock
// could both take it over; the file itself can't rule that out.
func Acquire(path string) (*Lock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, err
	}
	stale := 0
	for {
		created, err := create(path)
		if err != nil {
			return nil, err
		}
		if created {
			return &Lock{path: path, StalePID: stale}, nil
		}
		pid, err := holder(path)
		if errors.Is(err, os.ErrNotExist) {
			continue // its holder released it just now
		}
		if err != nil {
			return nil, err
		}
		if pid > 0 && alive(pid) {
			return nil, &HeldError{PID: pid, Path: path}
		}
		stale = pid
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
}

// create makes the lock file with this process's PID, reporting false if
// it already exists.
func create(path string) (bool, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // a fixed path in the runtime state
	if errors.Is(err, os.ErrExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	_, err = fmt.Fprintf(f, "%d\n", os.Getpid())
	return true, errors.Join(err, f.Close())
}

// holder reads the PID in the lock file, 0 if it never gets a valid one.
func holder(path string) (int, error) {
	for try := 0; ; try++ {
		data, err := os.ReadFile(path) //nolint:gosec // a fixed path in the runtime state
		if err != nil {
			return 0, err
		}
		if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
			return pid, nil
		}
		if try == writeTries {
			return 0, nil
		}
		time.Sleep(writeWait)
	}
}

// Release gives the lock up.
func (l *Lock) Release() error {
	return os.Remove(l.path)
}
