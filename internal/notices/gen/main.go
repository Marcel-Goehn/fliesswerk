// Command gen writes THIRD_PARTY_NOTICES at the repo root. It runs from
// internal/notices, through `go generate`.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Marcel-Goehn/fliesswerk/internal/notices"
)

func main() {
	text, err := notices.Render()
	if err == nil {
		err = os.WriteFile(filepath.Join("..", "..", notices.FileName), text, 0o644) //nolint:gosec // a public file in the repo
	}
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
