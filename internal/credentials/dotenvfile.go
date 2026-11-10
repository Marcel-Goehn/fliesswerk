package credentials

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// DotenvPath is root's .fliesswerk/.env.
func DotenvPath(root string) string {
	return filepath.Join(root, filepath.FromSlash(DotenvFile))
}

// DotenvValues are the keys of root's .fliesswerk/.env, none if there is no
// such file.
func DotenvValues(root string) (map[string]string, error) {
	opened, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer func() { _ = opened.Close() }()
	data, err := opened.ReadFile(DotenvFile)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	return parseDotenv(string(data)), nil
}

// AppendDotenv adds key=value to root's .fliesswerk/.env, unless the file
// has key already: it never changes or removes a line that is there. It
// refuses while git doesn't ignore the file, and creates it readable by the
// user alone. added says whether the line went in.
func AppendDotenv(ctx context.Context, root, key, value string) (added bool, err error) {
	if err := CheckDotenvIgnored(ctx, root); err != nil {
		return false, err
	}
	opened, err := os.OpenRoot(root)
	if err != nil {
		return false, err
	}
	defer func() { _ = opened.Close() }()
	if err := opened.MkdirAll(filepath.Dir(filepath.FromSlash(DotenvFile)), 0o750); err != nil {
		return false, err
	}
	data, err := opened.ReadFile(DotenvFile)
	exists := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	if _, ok := parseDotenv(string(data))[key]; ok {
		return false, nil
	}
	line := key + "=" + dotenvValue(value) + "\n"
	if len(data) > 0 && !strings.HasSuffix(string(data), "\n") {
		line = "\n" + line
	}
	var f *os.File
	if exists {
		f, err = opened.OpenFile(DotenvFile, os.O_WRONLY|os.O_APPEND, 0)
	} else {
		f, err = opened.OpenFile(DotenvFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			// Restricted before the secret goes in.
			if rerr := restrict(f); rerr != nil {
				_ = f.Close()
				return false, fmt.Errorf("can't make %s readable by you alone: %w", DotenvFile, rerr)
			}
		}
	}
	if err != nil {
		return false, err
	}
	_, werr := f.WriteString(line)
	if err := errors.Join(werr, f.Close()); err != nil {
		return false, fmt.Errorf("%s: %w", DotenvFile, err)
	}
	return true, nil
}

// dotenvValue renders v so that parseDotenv reads it back unchanged.
func dotenvValue(v string) string {
	if !strings.ContainsAny(v, " \t\n\r#\"'\\") {
		return v
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(v) + `"`
}
