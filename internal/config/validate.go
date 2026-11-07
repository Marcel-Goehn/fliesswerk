package config

import (
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// validateLayer checks one layer on its own against the schema: unknown
// keys, wrong types and values, local-only keys in config.toml, and
// secret-looking [sandbox.env] values.
func validateLayer(layer string, values map[string]any) []Diagnostic {
	c := &checker{layer: layer}
	c.table("", schema, values)
	c.secretLookingEnv(values)
	return c.diags
}

// validateMerged checks rules that span layers, on the merged config.
func validateMerged(values map[string]any) []Diagnostic {
	c := &checker{}

	steps, _ := values["steps"].(map[string]any)
	for _, name := range sortedKeys(steps) {
		step, _ := steps[name].(map[string]any)
		if _, hasHarness := step["harness"]; hasHarness {
			if _, hasModel := step["model"]; !hasModel {
				c.errorAt("steps."+name, "sets `harness` but not `model`; a Step that picks its own Harness must also pick its model, e.g. `model = \"…\"`")
			}
		}
	}

	sandbox, _ := values["sandbox"].(map[string]any)
	if sandbox["kind"] == "none" {
		if sandbox["i_accept_unsandboxed_risk"] != true {
			c.errorAt("sandbox.kind", "`kind = \"none\"` runs the agent with no Sandbox; it also needs `i_accept_unsandboxed_risk = true` in config.local.toml or `--i-accept-unsandboxed-risk`")
		} else {
			c.warnAt("sandbox.kind", "the agent runs with no Sandbox (`kind = \"none\"`): it can read and change anything your user can")
		}
	}
	network, _ := sandbox["network"].(map[string]any)
	if containsStar(network["allow"]) {
		c.warnAt("sandbox.network.allow", "`allow = [\"*\"]` lets the Sandbox reach any host")
	}

	commands, _ := values["commands"].(map[string]any)
	verify, _ := values["verify"].([]any)
	for i, v := range verify {
		name, _ := v.(string)
		if _, ok := commands[name]; !ok {
			c.errorAt(fmt.Sprintf("verify[%d]", i), fmt.Sprintf("%q is not a Feedback command in [commands]%s", name, didYouMean(name, sortedKeys(commands))))
		}
	}
	return c.diags
}

// checker collects the diagnostics of one layer, or of the merged config
// when layer is empty.
type checker struct {
	layer string
	diags []Diagnostic
}

// errorAt records an error about key.
func (c *checker) errorAt(key, msg string) {
	c.diags = append(c.diags, Diagnostic{Severity: Error, Layer: c.layer, Key: key, Message: msg})
}

// warnAt records a warning about key.
func (c *checker) warnAt(key, msg string) {
	c.diags = append(c.diags, Diagnostic{Severity: Warning, Layer: c.layer, Key: key, Message: msg})
}

// value checks v against n at the dotted key path.
func (c *checker) value(path string, n *node, v any) {
	if !c.typeOK(path, n, v) {
		return
	}
	if n.localOnly != nil && c.layer == ProjectFile && n.localOnly(v) {
		c.errorAt(path, fmt.Sprintf("%s is allowed only in %s or as a CLI flag, so that a committed change can't affect the whole team", n.why, LocalFile))
	}
	switch n.kind {
	case kTable:
		c.table(path, n, v.(map[string]any))
	case kSecret:
		c.table(path, secretSource, v.(map[string]any))
	case kList:
		for i, e := range v.([]any) {
			c.value(fmt.Sprintf("%s[%d]", path, i), n.elem, e)
		}
	}
}

// typeOK checks the type and the allowed values of v, reporting what's
// wrong.
func (c *checker) typeOK(path string, n *node, v any) bool {
	switch n.kind {
	case kString:
		s, ok := v.(string)
		if !ok {
			c.errorAt(path, "must be a string, not "+describe(v))
			return false
		}
		if n.enum != nil && !slices.Contains(n.enum, s) {
			c.errorAt(path, fmt.Sprintf("%q is not one of %s%s", s, quoteAll(n.enum), didYouMean(s, n.enum)))
			return false
		}
	case kVersion:
		i, ok := v.(int64)
		if !ok {
			c.errorAt(path, "must be a whole number, not "+describe(v))
			return false
		}
		if i != SupportedVersion {
			c.errorAt(path, fmt.Sprintf("must be %d, not %d", SupportedVersion, i))
			return false
		}
	case kInt:
		i, ok := v.(int64)
		if !ok {
			c.errorAt(path, "must be a whole number, not "+describe(v))
			return false
		}
		if n.positive && i < 1 {
			c.errorAt(path, fmt.Sprintf("must be at least 1, not %d", i))
			return false
		}
	case kBool:
		if _, ok := v.(bool); !ok {
			c.errorAt(path, "must be true or false, not "+describe(v))
			return false
		}
	case kDuration:
		s, ok := v.(string)
		d, err := time.ParseDuration(s)
		if !ok || err != nil || d <= 0 {
			c.errorAt(path, "must be a duration such as \"30s\", \"5m\" or \"1h30m\", not "+describe(v))
			return false
		}
	case kKeepDays:
		if i, ok := v.(int64); ok && i >= 1 || v == "forever" {
			return true
		}
		c.errorAt(path, "must be a positive whole number of days (`keep_days = 30`) or \"forever\" (`keep_days = \"forever\"`), not "+describe(v))
		return false
	case kSecret:
		if _, ok := v.(map[string]any); !ok {
			c.errorAt(path, "must say where the secret comes from, e.g. `{ stored = \"name\" }` or `{ env = \"NAME\" }`, not "+describeType(v)+"; the config never holds a secret itself")
			return false
		}
	case kList:
		if _, ok := v.([]any); !ok {
			c.errorAt(path, "must be a list, not "+describe(v))
			return false
		}
	case kTable:
		if _, ok := v.(map[string]any); !ok {
			c.errorAt(path, "must be a table, not "+describe(v))
			return false
		}
	}
	return true
}

// table checks the keys of the table t against n.
func (c *checker) table(path string, n *node, t map[string]any) {
	known := sortedKeys(n.fields)
	for _, k := range sortedKeys(t) {
		key := join(path, k)
		if f, ok := n.fields[k]; ok {
			before := len(c.diags)
			c.value(key, f, t[k])
			// A value of the wrong type may belong under a similar key.
			if !fits(f, t[k]) && len(c.diags) > before {
				c.diags[before].Message += didYouMean(k, keysThatFit(n, t[k]))
			}
			continue
		}
		hint := didYouMean(k, known)
		if n.extra == nil {
			c.errorAt(key, "unknown key"+hint)
			continue
		}
		// In a free-form table, a near-miss of a known key whose value
		// doesn't fit the free-form shape is most likely a typo.
		if hint != "" && !fits(n.extra, t[k]) {
			c.errorAt(key, "unknown key"+hint)
			continue
		}
		c.value(key, n.extra, t[k])
	}
	if n.oneOf != nil {
		var set []string
		for _, k := range n.oneOf {
			if _, ok := t[k]; ok {
				set = append(set, k)
			}
		}
		if len(set) != 1 {
			c.errorAt(path, fmt.Sprintf("must set exactly one of %s", quoteKeys(n.oneOf)))
		}
	}
}

// keysThatFit lists the known keys of the table n whose shape fits v.
func keysThatFit(n *node, v any) []string {
	var keys []string
	for _, k := range sortedKeys(n.fields) {
		if fits(n.fields[k], v) {
			keys = append(keys, k)
		}
	}
	return keys
}

// fits reports whether v has the basic type n expects.
func fits(n *node, v any) bool {
	switch v.(type) {
	case map[string]any:
		return n.kind == kTable || n.kind == kSecret
	case string:
		return n.kind == kString || n.kind == kDuration || n.kind == kKeepDays
	case int64:
		return n.kind == kInt || n.kind == kVersion || n.kind == kKeepDays
	case bool:
		return n.kind == kBool
	case []any:
		return n.kind == kList
	default:
		return true
	}
}

var (
	secretName  = regexp.MustCompile(`(?i)(^|_)(TOKEN|SECRET|PASSWORD|PASSWD|CREDENTIALS?|API_?KEY|PRIVATE_?KEY|ACCESS_?KEY)$`)
	secretValue = regexp.MustCompile(`^(gh[pousr]_|github_pat_|glpat-|sk-|xox[abpr]-|(AKIA|ASIA)[0-9A-Z]{16}$|eyJ[\w-]+\.[\w-]+\.|-----BEGIN )`)
)

// secretLookingEnv warns about [sandbox.env] values that look like secrets.
// The value itself is never repeated in the message.
func (c *checker) secretLookingEnv(values map[string]any) {
	sandbox, _ := values["sandbox"].(map[string]any)
	env, _ := sandbox["env"].(map[string]any)
	for _, name := range sortedKeys(env) {
		v, _ := env[name].(string)
		if v == "" || !looksLikeSecret(name, v) {
			continue
		}
		c.warnAt(join("sandbox.env", name), fmt.Sprintf("looks like a secret; [sandbox.env] is plain text in the config, so move it to [sandbox.secrets] as `%s = { env = \"%s\" }` (or stored, command, dotenv)", bareKey(name), name))
	}
}

// looksLikeSecret reports whether the environment variable name=v looks
// like it carries a secret, by its name or by the shape of its value.
func looksLikeSecret(name, v string) bool {
	return secretName.MatchString(name) || secretValue.MatchString(v) || hasPassword(v)
}

// hasPassword reports whether v is a URL with a password in it.
func hasPassword(v string) bool {
	u, err := url.Parse(v)
	if err != nil || u.User == nil {
		return false
	}
	_, ok := u.User.Password()
	return ok
}

// describeType names the type of a decoded TOML value without the value,
// for keys that might hold a secret by mistake.
func describeType(v any) string {
	switch v.(type) {
	case string:
		return "a string"
	case int64, float64:
		return "a number"
	case bool:
		return "true or false"
	default:
		return describe(v)
	}
}

// describe names the type of a decoded TOML value, with the value if short.
func describe(v any) string {
	switch x := v.(type) {
	case string:
		if len(x) > 40 {
			return "a string"
		}
		return fmt.Sprintf("the string %q", x)
	case int64:
		return fmt.Sprintf("the number %d", x)
	case float64:
		return "the number " + strconv.FormatFloat(x, 'g', -1, 64)
	case bool:
		return fmt.Sprintf("%t", x)
	case []any:
		return "a list"
	case map[string]any:
		return "a table"
	default:
		return "a date or time"
	}
}

// bare matches a key TOML allows unquoted.
var bare = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// bareKey renders k as a TOML key, quoting it when needed.
func bareKey(k string) string {
	if bare.MatchString(k) {
		return k
	}
	return strconv.Quote(k)
}

// join appends the key k to the dotted path.
func join(path, k string) string {
	if path == "" {
		return bareKey(k)
	}
	return path + "." + bareKey(k)
}

// quoteAll renders vs as a list of quoted strings.
func quoteAll(vs []string) string {
	q := make([]string, len(vs))
	for i, v := range vs {
		q[i] = strconv.Quote(v)
	}
	return strings.Join(q, ", ")
}

// quoteKeys renders vs as alternative keys: `a` or `b`.
func quoteKeys(vs []string) string {
	q := make([]string, len(vs))
	for i, v := range vs {
		q[i] = "`" + v + "`"
	}
	return strings.Join(q, " or ")
}

// sortedKeys returns the keys of m in order, for stable output.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
