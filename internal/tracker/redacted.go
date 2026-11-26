package tracker

import "context"

// Redacted is tr with every text it writes (comments, close comments, a
// pull request's title and body) passed through redact first, so no
// secret ever reaches the Tracker.
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

func (r redacted) OpenPullRequest(ctx context.Context, pr PullRequest) (string, error) {
	pr.Title, pr.Body = r.redact(pr.Title), r.redact(pr.Body)
	return r.Tracker.OpenPullRequest(ctx, pr)
}
