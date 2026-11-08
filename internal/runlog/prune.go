package runlog

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"time"
)

// Forever as keepDays keeps every Run's logs.
const Forever = 0

// runName is a Run directory's name: its start time, and a number if
// several Runs started in that second.
var runName = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}-\d{2}-\d{2}(-\d+)?$`)

// runs are the names of the Run directories, oldest first. Anything else
// in the directory isn't the Factory's and is left alone.
func runs(logs string) ([]string, error) {
	entries, err := os.ReadDir(logs)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() && runName.MatchString(e.Name()) {
			names = append(names, e.Name())
		}
	}
	slices.Sort(names)
	return names, nil
}

// Prune removes the Run directories that started more than keepDays days
// before now.
func Prune(root string, keepDays int, now time.Time) error {
	if keepDays == Forever {
		return nil
	}
	logs := filepath.Join(root, logsDir)
	names, err := runs(logs)
	if err != nil {
		return err
	}
	cutoff := now.AddDate(0, 0, -keepDays)
	var errs []error
	for _, name := range names {
		started, err := time.ParseInLocation(IDFormat, name[:len(IDFormat)], now.Location())
		if err != nil || !started.Before(cutoff) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(logs, name)); err != nil {
			errs = append(errs, fmt.Errorf("removing the logs of Run %s: %w", name, err))
		}
	}
	return errors.Join(errs...)
}

// Latest is the directory of the newest Run, false if there is none.
func Latest(root string) (string, bool) {
	logs := filepath.Join(root, logsDir)
	names, err := runs(logs)
	if err != nil || len(names) == 0 {
		return "", false
	}
	return filepath.Join(logs, names[len(names)-1]), true
}
