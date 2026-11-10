package credentials

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/Marcel-Goehn/kraftwerk/internal/bedrock"
	"github.com/Marcel-Goehn/kraftwerk/internal/harness"
)

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
	// Bedrock signs in to AWS, nil for every other method.
	Bedrock *bedrock.Client

	// baseURL is a gateway's, "" for every other method.
	baseURL     string
	rejectedFix string
	sources     []Source
	profile     string
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
		case f.Flag:
			if _, ok := v.(bool); !ok {
				problems = append(problems, key(f.Key)+"must be true or false")
			}
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
	if name == "bedrock" {
		problems = append(problems, checkBedrock(table, key)...)
	}
	return problems
}

// passThroughKey is the acknowledgement that hands a user's own AWS
// credentials to the Sandbox.
const passThroughKey = "i_accept_long_lived_keys_in_sandbox"

// checkBedrock checks what no single field can: where the credentials come
// from. Keys are one way and a profile another.
func checkBedrock(table map[string]any, key func(string) string) []string {
	_, profile := table["profile"]
	_, id := table["access_key_id"]
	_, secret := table["secret_access_key"]
	_, role := table["role_arn"]
	var problems []string
	switch {
	case profile && (id || secret):
		problems = append(problems, key("profile")+"set `profile` or the access keys, not both")
	case !profile && !id && !secret:
		problems = append(problems, key("method")+"method = \"bedrock\" needs `profile`, or `access_key_id` and `secret_access_key`")
	case id && !secret:
		problems = append(problems, key("secret_access_key")+"`access_key_id` also needs `secret_access_key`")
	case secret && !id:
		problems = append(problems, key("access_key_id")+"`secret_access_key` also needs `access_key_id`")
	}
	if pass, _ := table[passThroughKey].(bool); pass && role {
		problems = append(problems, key(passThroughKey)+"set `role_arn` or this, not both: a role is assumed with Bedrock-only credentials, which is the safer of the two")
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
	a := &Auth{Harness: desc.Name, Method: name, Env: map[string]string{}, rejectedFix: m.RejectedFix}
	fields := map[string]string{}
	for _, f := range m.Fields {
		v, set := table[f.Key]
		if !set {
			continue
		}
		if f.Flag {
			if on, _ := v.(bool); on {
				fields[f.Key] = "true"
			}
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
	a.baseURL = fields["base_url"]
	if desc.CredentialFile != nil && slices.Contains(desc.CredentialFile.Methods, name) {
		a.profile = fields["profile"]
		var err error
		a.Bedrock, err = bedrock.New(ctx, bedrock.Config{
			Region: fields["region"], Profile: fields["profile"],
			AccessKeyID: fields["access_key_id"], SecretAccessKey: fields["secret_access_key"],
			RoleARN: fields["role_arn"], PassThrough: fields[passThroughKey] == "true",
		})
		if err != nil {
			return nil, fmt.Errorf("harnesses.%s: %w; check `profile` and your AWS config", desc.Name, err)
		}
	}
	return a, nil
}

// TLSEndpoint is the host, with a port if it has its own, where the method's
// Harness talks TLS, "" if it doesn't. A gateway's `base_url` says more than
// Endpoints can: its scheme and port.
func (a *Auth) TLSEndpoint() string { return tlsEndpoint(a.Endpoints, a.baseURL) }

// MethodTLSEndpoint is the TLSEndpoint of m once its fields are known. It
// reads no secret, so fields needn't hold any.
func MethodTLSEndpoint(m harness.AuthMethod, fields map[string]string) string {
	var endpoints []string
	if m.Endpoints != nil {
		endpoints = m.Endpoints(fields)
	}
	return tlsEndpoint(endpoints, fields["base_url"])
}

func tlsEndpoint(endpoints []string, baseURL string) string {
	if len(endpoints) == 0 {
		return ""
	}
	u, err := url.Parse(baseURL)
	if err != nil || u.Hostname() != endpoints[0] {
		return endpoints[0]
	}
	if u.Scheme != "https" {
		return ""
	}
	return u.Host
}

// Test proves the AWS credentials the host mints the Sandbox's from, and
// says, in plain words, what to fix if they don't work. Every other method's
// credential is sent only by the Harness, inside the Sandbox, so the Factory
// never tests it (docs/adr/0007-only-the-harness-sends-its-credential.md).
func (a *Auth) Test(ctx context.Context) error {
	if !a.Tested() {
		return nil
	}
	if err := a.Bedrock.Test(ctx); err != nil {
		return fmt.Errorf("%s (%s): the AWS credentials don't work: %w; %s", a.Harness, a.Method, err, a.fix())
	}
	return nil
}

// Tested reports whether Test proves the credential ahead of time.
func (a *Auth) Tested() bool { return a.Bedrock != nil }

// RejectedFix says what to do when the Harness rejects the credential at a
// Step: the method's own fix, then where to replace it.
func (a *Auth) RejectedFix() string {
	if fix := a.fix(); fix != "" {
		return a.rejectedFix + "; then replace it: " + fix
	}
	return a.rejectedFix
}

// fix says how to replace the credential.
func (a *Auth) fix() string {
	if a.profile != "" {
		return fmt.Sprintf("check the AWS profile %q, and if it uses SSO run `aws sso login --profile %s`", a.profile, a.profile)
	}
	fixes := make([]string, len(a.sources))
	for i, s := range a.sources {
		fixes[i] = s.String() + ": " + s.fix()
	}
	return strings.Join(fixes, "; ")
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
