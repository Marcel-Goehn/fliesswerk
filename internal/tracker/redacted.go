package tracker

import "context"

// Redacted is tr with every text it writes (comments, close comments)
// passed through redact first, so no secret ever reaches the Tracker.
func Redacted(tr Tracker, redact func(string) string) Tracker {
	return redacted{Tracker: tr, redact: redact}
}

type redacted struct {
	Tracker
	redact func(string) string
}

func (r redacted) Comment(ctx context.Context, ticket ID, text string) error {
	return r.Tracker.Comment(ctx, ticket, r.redact(text))
}

func (r redacted) Close(ctx context.Context, ticket ID, text string) error {
	return r.Tracker.Close(ctx, ticket, r.redact(text))
}
