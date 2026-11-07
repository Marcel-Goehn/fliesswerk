// Package redact is the one redactor that sits in front of every output
// the Factory writes: the terminal, log files, saved prompts, the summary
// and Tracker comments. It replaces every known secret, in the forms it
// most often travels in (raw, base64 and URL-encoded), with ***.
//
// It can't see a secret that was encoded together with other text, e.g.
// the base64 of "user:secret" in a Basic auth header. Transcripts the
// Harness writes itself never pass through it.
package redact

import (
	"cmp"
	"encoding/base64"
	"io"
	"net/url"
	"slices"
	"strings"
	"sync"
)

// Mask is what every secret is replaced with.
const Mask = "***"

// Redactor replaces known secrets. It is safe for concurrent use, and
// secrets may be added at any time.
type Redactor struct {
	mu       sync.RWMutex
	forms    map[string]bool
	replacer *strings.Replacer
}

// New is a Redactor that knows secrets. Empty ones are ignored.
func New(secrets ...string) *Redactor {
	r := &Redactor{forms: map[string]bool{}}
	for _, s := range secrets {
		r.Add(s)
	}
	return r
}

// Add makes the Redactor replace secret, and its encoded forms, too.
func (r *Redactor) Add(secret string) {
	if secret == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, f := range forms(secret) {
		r.forms[f] = true
	}
	// Longest first: at any one position the Replacer takes the first
	// form that matches, and a secret may contain a shorter one.
	all := make([]string, 0, len(r.forms))
	for f := range r.forms {
		all = append(all, f)
	}
	slices.SortFunc(all, func(a, b string) int { return cmp.Or(len(b)-len(a), strings.Compare(a, b)) })
	pairs := make([]string, 0, 2*len(all))
	for _, f := range all {
		pairs = append(pairs, f, Mask)
	}
	r.replacer = strings.NewReplacer(pairs...)
}

// forms are the ways secret is commonly written out.
func forms(secret string) []string {
	b := []byte(secret)
	return []string{
		secret,
		base64.StdEncoding.EncodeToString(b),
		base64.URLEncoding.EncodeToString(b),
		base64.RawStdEncoding.EncodeToString(b),
		base64.RawURLEncoding.EncodeToString(b),
		url.QueryEscape(secret),
		url.PathEscape(secret),
	}
}

// String is s with every known secret replaced by Mask.
func (r *Redactor) String(s string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.replacer == nil {
		return s
	}
	return r.replacer.Replace(s)
}

// Leaks reports whether s still holds a known secret.
func (r *Redactor) Leaks(s string) bool {
	return r.String(s) != s
}

// Writer redacts everything written through it to w. It redacts each
// Write on its own, so a caller must hand it whole lines (as Fprintf
// does): a secret split across two Writes would get through.
func (r *Redactor) Writer(w io.Writer) io.Writer {
	return writer{r: r, w: w}
}

type writer struct {
	r *Redactor
	w io.Writer
}

func (w writer) Write(p []byte) (int, error) {
	if _, err := io.WriteString(w.w, w.r.String(string(p))); err != nil {
		return 0, err
	}
	return len(p), nil
}
