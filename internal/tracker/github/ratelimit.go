package github

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/Marcel-Goehn/fliesswerk/internal/tracker"
)

// secondaryLimitWait is how long to wait with no header to go by: GitHub
// says at least a minute.
const secondaryLimitWait = time.Minute

// write runs a gh command that changes the issue id. A rate limit comes
// back as a *tracker.RateLimitError.
func (t *Tracker) write(ctx context.Context, id tracker.ID, stdin []byte, args ...string) error {
	_, err := t.opts.Runner.Run(ctx, stdin, args...)
	if err == nil || !isRateLimit(err) {
		return err
	}
	return &tracker.RateLimitError{Wait: t.limitWait(ctx, id), Err: err}
}

// isRateLimit reports whether gh's error is about a rate limit.
func isRateLimit(err error) bool {
	msg := strings.ToLower(err.Error())
	for _, sign := range []string{"rate limit", "http 429", "submitted too quickly", "abuse detection"} {
		if strings.Contains(msg, sign) {
			return true
		}
	}
	return false
}

// limitWait is how long GitHub asks to be left alone. `gh issue` doesn't
// show the response's headers, so a read of the same issue, which the
// limit also applies to, is asked for them.
func (t *Tracker) limitWait(ctx context.Context, id tracker.ID) time.Duration {
	out, _ := t.opts.Runner.Run(ctx, nil, "api", "--include", t.apiPath(id.Key)) // a failure is what's expected
	return waitFrom(string(out), time.Now())
}

// waitFrom reads the wait out of a response's headers: Retry-After, then
// the primary limit's reset, and otherwise secondaryLimitWait.
func waitFrom(response string, now time.Time) time.Duration {
	headers := map[string]string{}
	for line := range strings.Lines(response) {
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		if name, value, ok := strings.Cut(line, ":"); ok {
			headers[strings.ToLower(name)] = strings.TrimSpace(value)
		}
	}
	if s, err := strconv.Atoi(headers["retry-after"]); err == nil {
		return time.Duration(s) * time.Second
	}
	if reset, err := strconv.ParseInt(headers["x-ratelimit-reset"], 10, 64); err == nil && headers["x-ratelimit-remaining"] == "0" {
		return max(time.Unix(reset, 0).Sub(now), 0)
	}
	return secondaryLimitWait
}
