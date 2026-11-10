package setup

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Marcel-Goehn/fliesswerk/internal/config"
	"github.com/Marcel-Goehn/fliesswerk/internal/credentials"
	"github.com/Marcel-Goehn/fliesswerk/internal/harness"
	"github.com/Marcel-Goehn/fliesswerk/internal/tracker"
)

// PersonalInput is what the personal questions are asked from.
type PersonalInput struct {
	Harness harness.Description
	Tracker tracker.Access
	// Repo is where the Tickets are, as the Tracker names it.
	Repo string
	// Store is the user-level credentials file.
	Store string
	// Home is the user's home directory, "" if unknown.
	Home string
	// FindInterception reports what a Sandbox wouldn't trust at host, nil
	// for nothing.
	FindInterception func(ctx context.Context, host string) *Interception
	// TeamCACerts are the team's `ca_certs`. They stay on the list this
	// person's own replaces.
	TeamCACerts []string
}

// Interception is a TLS endpoint a Sandbox wouldn't trust while this machine
// does: something on the way inspects TLS.
type Interception struct {
	Host string
	// Root is what this machine verified it against, nil if it didn't say.
	Root *x509.Certificate
}

// Personal are one person's answers. Nothing is saved until Save.
type Personal struct {
	harness string
	// auth are the lines of the Harness's table, tracker its token's source
	// ("" for the Tracker's own login), both rendered as TOML.
	auth    []string
	tracker string
	store   string
	secrets map[string]string
	// endpoint is where the Harness talks TLS, "" if it doesn't or it's unknown.
	endpoint string
	// caCerts are this person's `ca_certs`, nil to leave the team's alone.
	caCerts []string
	// export is the root to write to caFile, nil for none.
	export *x509.Certificate
	caFile string
}

// AskPersonal puts the personal questions: how this person signs in to the
// Harness and reaches the Tracker.
func AskPersonal(ctx context.Context, a Asker, in PersonalInput) (Personal, error) {
	a.say("\nNow your own settings. They stay on this machine.\n")
	p := Personal{harness: in.Harness.Name, store: in.Store, secrets: map[string]string{}}
	if err := a.signIn(ctx, in, &p); err != nil {
		return p, err
	}
	if err := a.reachTracker(ctx, in, &p); err != nil {
		return p, err
	}
	if err := a.offerRoot(ctx, in, &p); err != nil {
		return p, err
	}
	a.say("")
	return p, nil
}

// Save puts the typed secrets in the user-level credentials file and exports
// the root next to it.
func (p Personal) Save() error {
	for _, key := range slices.Sorted(maps.Keys(p.secrets)) {
		if err := credentials.Store(p.store, key, p.secrets[key]); err != nil {
			return err
		}
	}
	if p.export == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p.caFile), 0o700); err != nil {
		return err
	}
	data := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: p.export.Raw})
	out, err := os.OpenFile(p.caFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644) //nolint:gosec // a public certificate
	if errors.Is(err, fs.ErrExist) {
		return nil // named after its content, so it is this very root
	}
	if err != nil {
		return err
	}
	_, werr := out.Write(data)
	return errors.Join(werr, out.Close())
}

// LocalFile is this person's config.local.toml.
func LocalFile(p Personal, unsandboxed bool) File {
	var b strings.Builder
	b.WriteString("# Your own settings for this project. Never committed.\n")
	if len(p.auth) > 0 {
		fmt.Fprintf(&b, "\n[harnesses.%s]\n%s\n", tomlKey(p.harness), strings.Join(p.auth, "\n"))
	}
	if p.tracker != "" {
		fmt.Fprintf(&b, "\n[tracker]\ntoken = %s\n", p.tracker)
	}
	if len(p.caCerts) > 0 {
		fmt.Fprintf(&b, "\n[network]\nca_certs = %s\n", quoteList(p.caCerts))
	}
	if unsandboxed {
		b.WriteString("\n[sandbox]\nkind = \"none\"\ni_accept_unsandboxed_risk = true\n")
	}
	return File{config.LocalFile, b.String()}
}

// signIn asks for the Harness's auth method and each field it needs.
func (a Asker) signIn(ctx context.Context, in PersonalInput, p *Personal) error {
	m, values, err := a.authMethod(in)
	if err != nil {
		return err
	}
	if m.Hint != "" {
		a.say("  %s", m.Hint)
	}
	sources := map[string]string{}
	for _, f := range m.Fields {
		if _, answered := values[f.Key]; answered || f.Prompt == "" || values[f.UnlessSet] != "" {
			continue
		}
		if f.Secret {
			if sources[f.Key], err = a.keep(ctx, in, f, p); err != nil {
				return err
			}
			continue
		}
		ask := a.required
		if f.Optional {
			ask = a.text
		}
		answer, err := ask(f.Prompt, "")
		if err != nil {
			return err
		}
		if answer != "" && answer != none {
			values[f.Key] = answer
		}
	}
	p.endpoint = credentials.MethodTLSEndpoint(m, values)
	p.auth = []string{"method = " + quote(m.Name)}
	for _, f := range m.Fields {
		if v, ok := values[f.Key]; ok {
			p.auth = append(p.auth, f.Key+" = "+quote(v))
		} else if src, ok := sources[f.Key]; ok {
			p.auth = append(p.auth, f.Key+" = "+src)
		}
	}
	return nil
}

// offerRoot looks for TLS interception on the way to the Harness, and offers
// to have every Sandbox trust the root behind it. It has to be now: the image
// is built through that proxy, and config.local.toml is only ever created.
func (a Asker) offerRoot(ctx context.Context, in PersonalInput, p *Personal) error {
	if p.endpoint == "" {
		return nil
	}
	found := in.FindInterception(ctx, p.endpoint)
	if found == nil {
		return nil
	}
	a.say("A Sandbox wouldn't trust %s, though this machine does: something on the way, such as a corporate proxy, is inspecting TLS.", found.Host)
	if found.Root == nil {
		a.say("  This machine didn't say which root it trusts. Export your organisation's root as a PEM file and add it to `ca_certs` under [network] in config.local.toml.")
		return nil
	}
	sum := sha256.Sum256(found.Root.Raw)
	file := filepath.Join(filepath.Dir(in.Store), fmt.Sprintf("root-%x.pem", sum[:6]))
	export, err := a.yes(fmt.Sprintf("Export its root, %q, to %s and trust it in your Sandboxes?", found.Root.Subject.String(), file), true)
	if err != nil || !export {
		return err
	}
	p.export, p.caFile = found.Root, file
	p.caCerts = append(slices.Clone(in.TeamCACerts), file)
	return nil
}

// authMethod settles how this person signs in, and the fields that says.
func (a Asker) authMethod(in PersonalInput) (harness.AuthMethod, map[string]string, error) {
	d := in.Harness
	byName := func(name string) int {
		return slices.IndexFunc(d.AuthMethods, func(m harness.AuthMethod) bool { return m.Name == name })
	}
	if d.ImportAuth != nil && in.Home != "" {
		if found := d.ImportAuth(in.Home); found != nil && byName(found.Method) >= 0 {
			take, err := a.yes(found.Question, true)
			if err != nil {
				return harness.AuthMethod{}, nil, err
			}
			if take {
				return d.AuthMethods[byName(found.Method)], maps.Clone(found.Values), nil
			}
		}
	}
	options := make([]option, len(d.AuthMethods))
	for i, m := range d.AuthMethods {
		options[i] = option{m.Name, m.Label}
	}
	name, err := a.pick(d.AuthQuestion, options)
	if err != nil {
		return harness.AuthMethod{}, nil, err
	}
	return d.AuthMethods[byName(name)], map[string]string{}, nil
}

// reachTracker settles how this person reaches the Tracker: the login they
// already have, if they have one, or a token made for this repo alone.
func (a Asker) reachTracker(ctx context.Context, in PersonalInput, p *Personal) error {
	const useLogin, useToken = "login", "token"
	access := in.Tracker
	if access.LoggedIn(ctx) {
		how, err := a.pick("How should the Factory reach "+access.Name+"?", []option{{useLogin, access.LoginLabel}, {useToken, access.TokenLabel}})
		if err != nil || how == useLogin {
			return err
		}
	}
	a.say("%s", access.Walkthrough(in.Repo))
	var err error
	p.tracker, err = a.keep(ctx, in, harness.AuthField{Prompt: "the token", StoreKey: access.TokenKey(in.Repo)}, p)
	return err
}

// keep asks where the secret f is kept, and for the secret itself when it is
// to be stored under f.StoreKey. A NotStored secret is never asked for: it
// only says where the secret is. It returns the Source as TOML.
func (a Asker) keep(ctx context.Context, in PersonalInput, f harness.AuthField, p *Personal) (string, error) {
	options := []option{
		{credentials.Stored, "In your own credentials file, outside every repo (recommended)"},
		{credentials.Env, "In an environment variable"},
		{credentials.Command, "A command prints it (a password manager, say)"},
		{credentials.Dotenv, "In " + credentials.DotenvFile},
	}
	if f.NotStored != "" {
		options = slices.DeleteFunc(options, func(o option) bool { return o.value == credentials.Stored })
		a.say("  Not in your credentials file: %s.", f.NotStored)
	}
	where, err := a.pick("Where is "+f.Prompt+" kept?", options)
	if err != nil {
		return "", err
	}
	ref := f.StoreKey
	switch where {
	case credentials.Env:
		ref, err = a.required("Which variable holds it?", f.SuggestEnv)
	case credentials.Command:
		ref, err = a.required("Which command prints it?", "")
	case credentials.Dotenv:
		ref, err = a.required("Under which name is it in "+credentials.DotenvFile+"?", "")
	default:
		err = a.storeSecret(ctx, in, f.Prompt, f.StoreKey, p)
	}
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("{ %s = %s }", where, quote(ref)), nil
}

// storeSecret takes the secret to store under key, unless the one already
// there is to be used: the file is shared by every project.
func (a Asker) storeSecret(ctx context.Context, in PersonalInput, what, key string, p *Personal) error {
	if _, err := (credentials.Resolver{Store: in.Store}).Resolve(ctx, credentials.Source{Kind: credentials.Stored, Ref: key}); err == nil {
		reuse, err := a.yes(fmt.Sprintf("You already have %q in your credentials file. Use it? No replaces it for all your projects", key), true)
		if err != nil || reuse {
			return err
		}
	}
	secret, err := a.secret("Paste " + what)
	if err != nil {
		return err
	}
	p.secrets[key] = secret
	return nil
}
