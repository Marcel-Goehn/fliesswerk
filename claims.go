package main

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"time"

	"github.com/Marcel-Goehn/fliesswerk/internal/tracker"
)

// runRelease drops the Claim on the Spec or standalone Ticket the one
// argument names, so someone else's Run may take it.
func runRelease(args []string, stdout, stderr io.Writer) int {
	const cmd = "fliesswerk release"
	values, root, rest, code, ok := loadConfig(cmd, args, 1, stderr, nil)
	if !ok {
		return code
	}
	if len(rest) != 1 {
		_, _ = fmt.Fprintf(stderr, "%s: name the Spec or standalone Ticket to release, e.g. `fliesswerk release 42`\n", cmd)
		return exitConfigError
	}
	s, code, ok := secretsForRun(values, root, stderr)
	if !ok {
		return code
	}
	stdout, stderr = s.redacted(stdout, stderr)
	tr, err := newTracker(values, s.trackerToken)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s: %v\n", cmd, err)
		return exitConfigError
	}
	ctx := context.Background()
	claims, err := tr.Claims(ctx)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s: %v\n", cmd, err)
		return exitStopped
	}
	for _, c := range claims {
		if !c.ID.Names(rest[0]) {
			continue
		}
		if err := tr.Release(ctx, c.ID, c.Claim.By); err != nil {
			_, _ = fmt.Fprintf(stderr, "%s: %v\n", cmd, err)
			return exitStopped
		}
		_, _ = fmt.Fprintf(stdout, "Released %s.\n", claimLine(c))
		return exitOK
	}
	_, _ = fmt.Fprintf(stderr, "%s: %s isn't claimed; there is nothing to release\n", cmd, rest[0])
	return exitFailures
}

// claimedByOthers are the open issues someone other than the Tracker's
// user claimed.
func claimedByOthers(ctx context.Context, tr tracker.Tracker) ([]tracker.Claimed, error) {
	user, err := tr.User(ctx)
	if err != nil {
		return nil, err
	}
	claims, err := tr.Claims(ctx)
	if err != nil {
		return nil, err
	}
	var out []tracker.Claimed
	for _, c := range claims {
		if c.Claim.By != user {
			out = append(out, c)
		}
	}
	return out, nil
}

// claimLine shows a Claim: the issue, its title, the claimer and the
// date, if it is known.
func claimLine(c tracker.Claimed) string {
	line := fmt.Sprintf("%s  %s: claimed by %s", c.ID, c.Title, cmp.Or(c.Claim.By, "someone unknown"))
	if !c.Claim.At.IsZero() {
		line += " on " + c.Claim.At.Format(time.DateOnly)
	}
	return line
}

// printClaimedByOthers prints the Specs a Run skips because someone else
// claimed them, if there are any.
func printClaimedByOthers(w io.Writer, claims []tracker.Claimed) {
	if len(claims) == 0 {
		return
	}
	_, _ = fmt.Fprintln(w, "\nClaimed by others, skipped:")
	for _, c := range claims {
		_, _ = fmt.Fprintf(w, "  %s\n", claimLine(c))
	}
}
