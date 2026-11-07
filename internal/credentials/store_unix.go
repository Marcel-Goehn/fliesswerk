//go:build !windows

package credentials

import (
	"fmt"
	"os"
)

// restrict makes f readable and writable by its owner alone.
func restrict(f *os.File) error {
	return f.Chmod(0o600)
}

// checkPrivate refuses a credentials file anyone but its owner may read
// or write, as ssh does with a private key.
func checkPrivate(f *os.File, path string) error {
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%s can be read or changed by other users (mode %04o); run `chmod 600 %s`", path, fi.Mode().Perm(), path)
	}
	return nil
}
