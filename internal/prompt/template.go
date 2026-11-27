package prompt

import (
	"embed"
	"strings"
	"text/template"

	"github.com/Marcel-Goehn/fliesswerk/internal/tracker"
	"github.com/Marcel-Goehn/fliesswerk/internal/verify"
)

// Names of the Steps that have a prompt.
const (
	implementStep = "implement"
	reviewStep    = "review"
	mergeStep     = "merge"
)

// Steps are the Steps with a prompt, in the order a Ticket meets them.
var Steps = []string{implementStep, reviewStep, mergeStep}

// Each Step's whole prompt, templates/<step>.md.
//
//go:embed templates
var templateFS embed.FS

// templates are every Step's parsed template, by Step.
var templates = loadTemplates()

// loadTemplates parses every Step's template, with "\n" line ends however
// git checked it out.
func loadTemplates() map[string]*template.Template {
	out := map[string]*template.Template{}
	for _, step := range Steps {
		text, err := templateFS.ReadFile("templates/" + step + ".md")
		if err != nil {
			panic(err) // the templates are compiled in
		}
		out[step] = parse(step, strings.ReplaceAll(string(text), "\r\n", "\n"))
	}
	return out
}

// parse parses a Step's template. A template that doesn't parse is a
// programming error.
func parse(name, text string) *template.Template {
	return template.Must(template.New(name).Option("missingkey=error").Parse(text))
}

// render renders t with data. A field t names that data lacks is an
// error, not an empty string.
func render(t *template.Template, data any) (string, error) {
	var b strings.Builder
	err := t.Execute(&b, data)
	return b.String(), err
}

// mustRender renders step's template. The data is the Factory's own, so
// a failure is a bug in the template, which the golden tests catch.
func mustRender(step string, data any) string {
	out, err := render(templates[step], data)
	if err != nil {
		panic(err)
	}
	return out
}

// view is what every Step's template gets.
type view struct {
	Ticket    ticketView
	Comments  []commentView
	Spec      *specView
	Branch    string
	Protected []string
	Commands  []verify.Command
	Services  []serviceView
}

type ticketView struct {
	ID, Title, Body string
}

type commentView struct {
	Author, Date, Body string
}

type specView struct {
	ID, Title, Body string
	Tickets         []specTicketView
}

type specTicketView struct {
	ID, Title string
	// Mark is how the Spec's Ticket list marks the Ticket.
	Mark string
}

type serviceView struct {
	Name      string
	Addresses []string
}

// implementData is what implement's template gets.
type implementData struct {
	view
	Recent recentView
	// Failed is the failed verify a retry is told about, nil for none.
	Failed *failedView
}

type recentView struct {
	BaseBranch   string
	OnSpecBranch bool
	Commits      []recentCommit
}

type recentCommit struct {
	Short, Text string
}

type failedView struct {
	Name, Line, Err string
	// Fence is the code fence around Tail.
	Fence string
	// Tail is the failed command's last lines of output.
	Tail string
}

// reviewData is what review's template gets.
type reviewData struct {
	view
	// Standards is the Coding standards file, "" for none.
	Standards string
	// Base is the commit the change to review was made on top of.
	Base string
}

// mergeData is what merge's template gets.
type mergeData struct {
	view
	SpecBranch string
	Files      []string
	Merged     []mergedView
}

type mergedView struct {
	ID, Title, Body string
	Commits         []mergedCommit
}

type mergedCommit struct {
	Short, Subject string
}

// viewOf prepares what every Step's template gets from c.
func viewOf(c Context) view {
	v := view{
		Ticket:    ticketView{ID: c.Ticket.ID.String(), Title: c.Ticket.Title, Body: strings.TrimSpace(c.Ticket.Body)},
		Branch:    c.Branch,
		Protected: c.Protected,
		Commands:  c.Commands,
	}
	for _, cm := range c.Comments {
		v.Comments = append(v.Comments, commentView{
			Author: cm.Author,
			Date:   cm.CreatedAt.UTC().Format("2006-01-02 15:04 UTC"),
			Body:   strings.TrimSpace(cm.Body),
		})
	}
	if s := c.Spec; s != nil {
		v.Spec = &specView{ID: s.ID.String(), Title: s.Title, Body: strings.TrimSpace(s.Body)}
		for _, tk := range s.Tickets {
			v.Spec.Tickets = append(v.Spec.Tickets, specTicketView{ID: tk.ID.String(), Title: tk.Title, Mark: mark(tk, c)})
		}
	}
	for _, s := range c.Services {
		v.Services = append(v.Services, serviceView{Name: s.Name, Addresses: s.Addresses})
	}
	return v
}

// mark is how the Spec's Ticket list marks tk.
func mark(tk tracker.SpecTicket, c Context) string {
	switch {
	case tk.ID.Key == c.Ticket.ID.Key:
		return "this Ticket"
	case tk.State == tracker.Closed:
		return "closed"
	case c.InProgress[tk.ID.Key]:
		return "in progress"
	}
	return "open"
}

// recentOf prepares the commits implement builds on.
func recentOf(r Recent) recentView {
	v := recentView{BaseBranch: r.BaseBranch, OnSpecBranch: r.OnSpecBranch}
	for _, m := range r.Commits {
		v.Commits = append(v.Commits, recentCommit{Short: m.Short(), Text: m.Text})
	}
	return v
}
