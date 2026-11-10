package credentials

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/pelletier/go-toml/v2"
)

// StorePath is the user-level credentials file: outside every repo, shared
// across projects, readable by the user alone.
func StorePath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("can't find your config directory: %w", err)
	}
	return filepath.Join(dir, "fliesswerk", "credentials"), nil
}

var storeKey = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// ValidKey reports whether key may name a stored secret.
func ValidKey(key string) bool { return storeKey.MatchString(key) }

// Store sets key to value in the credentials file at path, creating it
// (and its directory) readable by the user alone. The file is replaced in
// one step, so a crash never leaves it half-written.
func Store(path, key, value string) error {
	if !ValidKey(key) {
		return fmt.Errorf("%q can't name a secret; use letters, digits, '.', '_' and '-'", key)
	}
	if value == "" {
		return errors.New("the secret is empty")
	}
	values, err := readStore(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		values = map[string]string{}
	case err != nil:
		return err
	}
	values[key] = value
	data, err := toml.Marshal(values)
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".credentials-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }() // a no-op once renamed
	// Restricted before the secret goes in.
	if err := restrict(f); err != nil {
		_ = f.Close()
		return fmt.Errorf("can't make %s readable by you alone: %w", tmp, err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// readStore reads the credentials file, refusing one others can read.
func readStore(path string) (map[string]string, error) {
	f, err := os.Open(path) //nolint:gosec // the user's own credentials file
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	if err := checkPrivate(f, path); err != nil {
		return nil, err
	}
	values := map[string]string{}
	if err := toml.NewDecoder(f).Decode(&values); err != nil {
		return nil, fmt.Errorf("%s is not a valid credentials file: %w", path, err)
	}
	return values, nil
}
