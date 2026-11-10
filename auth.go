package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/Marcel-Goehn/kraftwerk/internal/credentials"
)

const authUsage = "Usage: kraftwerk auth set <key>\n"

// runAuth runs `kraftwerk auth set <key>`: it reads a secret from a
// hidden prompt (or one line of stdin, when stdin isn't a terminal) and
// stores it under key in the user-level credentials file.
func runAuth(args []string, stdin *os.File, stdout, stderr io.Writer) int {
	if len(args) != 2 || args[0] != "set" {
		_, _ = fmt.Fprint(stderr, authUsage)
		return exitConfigError
	}
	key := args[1]
	if !credentials.ValidKey(key) {
		_, _ = fmt.Fprintf(stderr, "kraftwerk auth set: %q can't name a secret; use letters, digits, '.', '_' and '-'\n", key)
		return exitConfigError
	}
	if f, refused := credentials.NotStoredKey(theHarness.Describe(), key); refused {
		_, _ = fmt.Fprintf(stderr, "kraftwerk auth set: nothing was stored: %s\n", credentials.NotStoredFix(f))
		return exitConfigError
	}
	path, err := credentials.StorePath()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "kraftwerk auth set: %v\n", err)
		return exitConfigError
	}
	secret, err := readSecret(stdin, stderr, key)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "kraftwerk auth set: %v\n", err)
		return exitConfigError
	}
	if secret == "" {
		_, _ = fmt.Fprintln(stderr, "kraftwerk auth set: the secret is empty; nothing was stored")
		return exitConfigError
	}
	if err := credentials.Store(path, key, secret); err != nil {
		_, _ = fmt.Fprintf(stderr, "kraftwerk auth set: %v\n", err)
		return exitConfigError
	}
	_, _ = fmt.Fprintf(stdout, "Stored %q in %s.\nReference it in .kraftwerk/config.local.toml as `secret = { stored = %q }`.\n", key, path, key)
	return exitOK
}

// readSecret reads the secret without echoing it from a terminal, or the
// first line of a pipe.
func readSecret(stdin *os.File, prompt io.Writer, key string) (string, error) {
	fd := int(stdin.Fd()) //nolint:gosec // a file descriptor fits an int
	if term.IsTerminal(fd) {
		_, _ = fmt.Fprintf(prompt, "Secret for %q (input is hidden): ", key)
		b, err := term.ReadPassword(fd)
		_, _ = fmt.Fprintln(prompt)
		return strings.TrimSpace(string(b)), err
	}
	line, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	return strings.TrimSpace(line), nil
}
