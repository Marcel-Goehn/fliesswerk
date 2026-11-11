package setup

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/term"

	"github.com/Marcel-Goehn/fliesswerk/internal/tracker"
)

// none is what a person types to clear a suggestion.
const none = "-"

// Sandbox kinds.
const (
	kindContainer = "container"
	kindNative    = "native"
	kindNone      = "none"
)

// Answers are what `init` settled on.
type Answers struct {
	Tracker string
	// Values are the Tracker questions' answers, by config key.
	Values map[string]string
	// Languages are ids, in recipe order.
	Languages []string
	// Commands are Feedback commands by name; only those that are set.
	Commands      map[string]string
	ComposeFile   string
	Services      []Service
	Standards     string
	ProtectClaude bool
}

// Asker puts questions to a person, one line per answer.
type Asker struct {
	in  *bufio.Reader
	out io.Writer
	// terminal is where a secret is read from without echoing it, nil when
	// the input isn't a terminal.
	terminal *os.File
}

// NewAsker asks out and reads the answers from in.
func NewAsker(in io.Reader, out io.Writer) Asker {
	a := Asker{in: bufio.NewReader(in), out: out}
	if f, ok := in.(*os.File); ok && term.IsTerminal(int(f.Fd())) { //nolint:gosec // a file descriptor fits an int
		a.terminal = f
	}
	return a
}

// say prints one line.
func (a Asker) say(format string, args ...any) { _, _ = fmt.Fprintf(a.out, format+"\n", args...) }

// line reads one answer. Running out of input stops `init`: taking the
// suggestions instead would be the --yes it doesn't have.
func (a Asker) line() (string, error) {
	text, err := a.in.ReadString('\n')
	if errors.Is(err, io.EOF) && text == "" {
		return "", errors.New("the input ended before the last question; nothing was written")
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimSpace(text), nil
}

// text asks one question: Enter takes the suggestion.
func (a Asker) text(question, suggestion string) (string, error) {
	if suggestion != "" {
		question += " [" + suggestion + "]"
	}
	_, _ = fmt.Fprint(a.out, question+": ")
	answer, err := a.line()
	if err != nil || answer != "" {
		return answer, err
	}
	return suggestion, nil
}

// required asks until it has an answer.
func (a Asker) required(question, suggestion string) (string, error) {
	for {
		answer, err := a.text(question, suggestion)
		if err != nil || answer != "" {
			return answer, err
		}
		a.say("  This one needs an answer.")
	}
}

// secret asks until it has a secret, hidden from a terminal.
func (a Asker) secret(question string) (string, error) {
	for {
		answer, err := a.hidden(question)
		if err != nil || answer != "" {
			return answer, err
		}
		a.say("  This one needs an answer.")
	}
}

// hidden asks for a secret once, hidden from a terminal; Enter leaves it
// empty.
func (a Asker) hidden(question string) (string, error) {
	_, _ = fmt.Fprint(a.out, question+" (input is hidden): ")
	return a.hiddenLine()
}

// wait asks for Enter and waits for it. Input that has ended counts as one.
func (a Asker) wait(question string) {
	_, _ = fmt.Fprint(a.out, question+": ")
	_, _ = a.in.ReadString('\n')
	a.say("")
}

// hiddenLine reads one line without echoing it, if there is an echo to stop.
func (a Asker) hiddenLine() (string, error) {
	if a.terminal == nil {
		return a.line()
	}
	b, err := term.ReadPassword(int(a.terminal.Fd())) //nolint:gosec // a file descriptor fits an int
	a.say("")
	return strings.TrimSpace(string(b)), err
}

// option is one choice of a select question.
type option struct{ value, label string }

// pick asks a select question, by number or by value; Enter takes the
// first option.
func (a Asker) pick(question string, options []option) (string, error) {
	a.say("%s", question)
	for i, o := range options {
		a.say("  %d) %s", i+1, o.label)
	}
	for {
		answer, err := a.text("Choice", "1")
		if err != nil {
			return "", err
		}
		for i, o := range options {
			if answer == fmt.Sprint(i+1) || answer == o.value {
				return o.value, nil
			}
		}
		a.say("  Type a number from 1 to %d.", len(options))
	}
}

// yes asks a yes-or-no question.
func (a Asker) yes(question string, def bool) (bool, error) {
	suggestion := "y/N"
	if def {
		suggestion = "Y/n"
	}
	for {
		answer, err := a.text(question, suggestion)
		if err != nil {
			return false, err
		}
		switch strings.ToLower(answer) {
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		case strings.ToLower(suggestion):
			return def, nil
		}
		a.say("  Type y or n.")
	}
}

// Ask puts the project questions, in their fixed order, and settles the
// answers. Enter takes what the repo suggests.
func Ask(root string, a Asker, f Facts, questions []tracker.Question) (Answers, error) {
	a.say("Setting up this project. Press Enter to take the suggestion in brackets, or type - to leave it out.\n")
	var ans Answers
	var err error

	if ans.Tracker, err = a.pick("Where do your Tickets live?", []option{{"github", "GitHub Issues"}}); err != nil {
		return ans, err
	}
	suggestions := map[string]string{"tracker.repo": f.Repo, "branches.base_branch": f.BaseBranch}
	ans.Values = map[string]string{}
	for _, q := range questions {
		if ans.Values[q.Key], err = a.required(q.Prompt, orElse(suggestions[q.Key], q.Default)); err != nil {
			return ans, err
		}
	}
	if ans.Languages, err = a.languages(f); err != nil {
		return ans, err
	}
	if ans.Commands, err = a.commands(f.commandSuggestions(ans.Languages)); err != nil {
		return ans, err
	}
	if ans.ComposeFile, err = a.file(root, "Which compose file starts the Services the agent needs (a database, say)?", f.Compose); err != nil {
		return ans, err
	}
	if ans.Services, err = services(root, ans.ComposeFile); err != nil {
		return ans, err
	}
	if ans.Standards, err = a.file(root, "Which file holds your Coding standards? Only the review Step reads it", f.Standards); err != nil {
		return ans, err
	}
	if f.HasClaudeDir {
		if ans.ProtectClaude, err = a.yes("Stop agents from changing .claude/? A planted hook there would run later", true); err != nil {
			return ans, err
		}
	}
	a.say("")
	return ans, nil
}

// services are the Services of the chosen compose file, none for "".
func services(root, file string) ([]Service, error) {
	if file == "" {
		return nil, nil
	}
	opened, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer func() { _ = opened.Close() }()
	list, err := readServices(repo{opened}, file)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	return list, nil
}

// languages asks which languages the project uses.
func (a Asker) languages(f Facts) ([]string, error) {
	var ids []string
	a.say("What does your project use? Install anything else yourself in the Containerfile.")
	for _, r := range recipes {
		ids = append(ids, r.id)
		a.say("  %-7s %s", r.id, r.label)
	}
	if len(f.Unknown) > 0 {
		a.say("  (found %s, which has no recipe)", strings.Join(f.Unknown, ", "))
	}
	for {
		answer, err := a.text("Languages, separated by commas", strings.Join(f.Languages(), ","))
		if err != nil {
			return nil, err
		}
		chosen, bad := parseLanguages(answer, ids)
		if bad == "" {
			return chosen, nil
		}
		a.say("  %q isn't one of %s.", bad, strings.Join(ids, ", "))
	}
}

// parseLanguages reads a list of language ids into recipe order; bad is
// the first that isn't one.
func parseLanguages(answer string, ids []string) (chosen []string, bad string) {
	if answer == none {
		return nil, ""
	}
	typed := strings.FieldsFunc(answer, func(r rune) bool { return r == ',' || r == ' ' })
	for _, t := range typed {
		if !slices.Contains(ids, t) {
			return nil, t
		}
	}
	for _, id := range ids {
		if slices.Contains(typed, id) {
			chosen = append(chosen, id)
		}
	}
	return chosen, ""
}

// commands asks for each well-known Feedback command.
func (a Asker) commands(suggested map[string]string) (map[string]string, error) {
	a.say("Which commands tell the agent whether its change works?")
	out := map[string]string{}
	for _, name := range wellKnownCommands {
		line, err := a.text("  "+name, suggested[name])
		if err != nil {
			return nil, err
		}
		if line != "" && line != none {
			out[name] = line
		}
	}
	return out, nil
}

// file asks for a file in the project; "" means none.
func (a Asker) file(root, question, suggestion string) (string, error) {
	for {
		answer, err := a.text(question+" (a path, Enter or - for none)", suggestion)
		if err != nil || answer == "" || answer == none {
			return "", err
		}
		rel := filepath.FromSlash(answer)
		if _, err := os.Stat(filepath.Join(root, rel)); err == nil && filepath.IsLocal(rel) {
			return answer, nil
		}
		a.say("  There's no such file in this project.")
	}
}

// sandbox asks where the agent runs, a second time before none, and then
// for the container engine wherever something runs in one.
func (a Asker) sandbox(in PersonalInput, p *Personal) error {
	kind, err := a.pick("Where should the agent run?", []option{
		{kindContainer, "In a container (recommended). Needs Docker or Podman. The agent sees only the project."},
		{kindNative, "Directly on this machine, fenced in by the operating system. Linux, macOS, WSL2."},
		{kindNone, "Directly on this machine, not fenced in at all."},
	})
	if err != nil {
		return err
	}
	if kind == kindNone {
		sure, err := a.yes("With no Sandbox the agent can delete your files, read your SSH keys and send them anywhere. Run it that way on this machine?", false)
		if err != nil {
			return err
		}
		if sure {
			p.unsandboxed = true
			a.say("  Only for you: that goes into config.local.toml, never into the team's config.toml.")
		} else {
			kind = kindContainer
			a.say("  Staying with the container Sandbox.")
		}
	}
	p.kind = kind
	question := "Which container engine?"
	if kind != kindContainer {
		if !in.HasServices {
			return nil
		}
		question = "Your Services run in containers. Which container engine?"
	}
	p.engine, err = a.pick(question, []option{
		{engineDocker, "Docker (recommended)" + installedNote(in, engineDocker)},
		{enginePodman, "Podman" + installedNote(in, enginePodman)},
	})
	return err
}

// Container engines.
const (
	engineDocker = "docker"
	enginePodman = "podman"
)

// installedNote says whether an engine is on this machine.
func installedNote(in PersonalInput, engine string) string {
	if in.Installed != nil && in.Installed(engine) {
		return ", installed"
	}
	return ", not installed: `fliesswerk check` names the command that installs it"
}
