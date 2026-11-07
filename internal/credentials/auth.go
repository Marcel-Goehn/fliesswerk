package credentials

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/Marcel-Goehn/kraftwerk/internal/harness"
)

// unavailable are auth methods a Harness may declare that this kraftwerk
// can't deliver yet, with what to say instead.
var unavailable = map[string]string{
	"bedrock": "method = \"bedrock\" isn't available in this kraftwerk yet; use oauth_token, api_key or gateway",
}

// Auth is a Harness's credential, resolved for a Run.
type Auth struct {
	Harness, Method string
	// Env is what each Step's env gets: the credential and the method's
	// other settings.
	Env map[string]string
	// Secrets are the resolved secret values, for the redactor.
	Secrets []string
	// Endpoints are the hosts the method talks to.
	Endpoints []string

	test    harness.Request
	sources []Source
}

// methodOf finds the method the table names in desc, nil if the Harness
// doesn't declare it.
func methodOf(desc harness.Description, name string) *harness.AuthMethod {
	for i := range desc.AuthMethods {
		if desc.AuthMethods[i].Name == name {
			return &desc.AuthMethods[i]
		}
	}
	return nil
}

// CheckAuth checks a Harness's `[harnesses.<name>]` table against the auth
// methods the Harness declares, and returns every problem. An empty table
// is fine: the Harness then uses its own login.
func CheckAuth(desc harness.Description, table map[string]any) []string {
	if len(table) == 0 {
		return nil
	}
	key := func(k string) string { return "harnesses." + desc.Name + "." + k + ": " }
	name, _ := table["method"].(string)
	if name == "" {
		return []string{key("method") + "set `method` to say how " + desc.Name + " signs in, e.g. `method = \"oauth_token\"`"}
	}
	m := methodOf(desc, name)
	if m == nil {
		names := make([]string, len(desc.AuthMethods))
		for i, am := range desc.AuthMethods {
			names[i] = fmt.Sprintf("%q", am.Name)
		}
		return []string{key("method") + fmt.Sprintf("%s doesn't accept method = %q; use %s", desc.Name, name, strings.Join(names, ", "))}
	}
	if why, ok := unavailable[name]; ok {
		return []string{key("method") + why}
	}

	var problems []string
	for _, k := range sortedKeys(table) {
		if k != "method" && !slices.ContainsFunc(m.Fields, func(f harness.AuthField) bool { return f.Key == k }) {
			problems = append(problems, key(k)+fmt.Sprintf("`%s` isn't used with method = %q; remove it", k, name))
		}
	}
	for _, f := range m.Fields {
		v, set := table[f.Key]
		switch {
		case !set && !f.Optional:
			problems = append(problems, key(f.Key)+fmt.Sprintf("method = %q needs `%s`", name, f.Key))
		case !set:
		case f.Secret:
			if _, ok := ParseSource(v); !ok {
				problems = append(problems, key(f.Key)+"must say where the secret comes from, e.g. `{ stored = \"name\" }`")
			}
		case f.Key == "base_url":
			if s, _ := v.(string); !isHTTPURL(s) {
				problems = append(problems, key(f.Key)+"base_url must be an http or https URL, e.g. \"https://llm.example.com\"")
			}
		}
	}
	return problems
}

func isHTTPURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != ""
}

// ResolveAuth resolves the credential of a Harness's `[harnesses.<name>]`
// table. It is nil when the table is empty: the Harness then signs in
// with its own login.
func ResolveAuth(ctx context.Context, r Resolver, desc harness.Description, table map[string]any) (*Auth, error) {
	if len(table) == 0 {
		return nil, nil
	}
	if problems := CheckAuth(desc, table); len(problems) > 0 {
		return nil, errors.New(strings.Join(problems, "\n"))
	}
	name, _ := table["method"].(string)
	m := methodOf(desc, name)
	a := &Auth{Harness: desc.Name, Method: name, Env: map[string]string{}}
	fields := map[string]string{}
	for _, f := range m.Fields {
		v, set := table[f.Key]
		if !set {
			continue
		}
		if !f.Secret {
			fields[f.Key], _ = v.(string)
			continue
		}
		src, _ := ParseSource(v)
		secret, err := r.Resolve(ctx, src)
		if err != nil {
			return nil, fmt.Errorf("harnesses.%s.%s: %w", desc.Name, f.Key, err)
		}
		fields[f.Key] = secret
		a.Secrets = append(a.Secrets, secret)
		a.sources = append(a.sources, src)
	}
	for env, ref := range m.Env {
		if fixed, ok := strings.CutPrefix(ref, "="); ok {
			a.Env[env] = fixed
		} else if v, ok := fields[ref]; ok {
			a.Env[env] = v
		}
	}
	if m.Endpoints != nil {
		a.Endpoints = m.Endpoints(fields)
	}
	if m.TestCall != nil {
		a.test = m.TestCall(fields)
	}
	return a, nil
}

// Test makes the method's one free test call and says, in plain words,
// what to fix if the credential doesn't work.
func (a *Auth) Test(ctx context.Context, client *http.Client) error {
	t := a.test
	if t.URL == "" {
		return nil
	}
	if t.SignAWS != "" {
		return fmt.Errorf("%s (%s): signed test calls aren't available in this kraftwerk yet", a.Harness, a.Method)
	}
	req, err := http.NewRequestWithContext(ctx, t.Method, t.URL, nil)
	if err != nil {
		return fmt.Errorf("%s (%s): the test call is not valid: %w", a.Harness, a.Method, err)
	}
	for k, v := range t.Header {
		req.Header.Set(k, v)
	}
	host := req.URL.Host
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%s (%s): can't reach %s to test the credential: %v; check your network or proxy", a.Harness, a.Method, host, unwrapURLError(err))
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	_ = resp.Body.Close()
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nil
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("%s (%s): %s refused the credential (HTTP %d); replace it: %s", a.Harness, a.Method, host, resp.StatusCode, a.fix())
	default:
		return fmt.Errorf("%s (%s): %s answered the test call with HTTP %d; check that it is the right endpoint, or try again later", a.Harness, a.Method, host, resp.StatusCode)
	}
}

// unwrapURLError drops the method and URL a *url.Error repeats.
func unwrapURLError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// fix says how to replace the credential.
func (a *Auth) fix() string {
	fixes := make([]string, len(a.sources))
	for i, s := range a.sources {
		fixes[i] = s.String() + ": " + s.fix()
	}
	hint := ""
	if a.Method == "oauth_token" {
		hint = " (get a new token with `claude setup-token`)"
	}
	return strings.Join(fixes, "; ") + hint
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
