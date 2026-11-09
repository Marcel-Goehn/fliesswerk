package config

// The config's shape, as one tree. It drives validation and the published
// JSON Schema alike, so the two can't drift apart. Keys may be accepted here
// before the feature behind them exists.

// kind is the type of value a node accepts.
type kind int

const (
	kString  kind = iota
	kVersion      // the config format version
	kInt
	kBool
	kDuration // a string such as "5m"
	kKeepDays // a positive whole number or "forever"
	kSecret   // a reference to where a secret comes from, never its value
	kList
	kTable
)

// node is the shape of one key: its type, its allowed values and, for
// tables and lists, the shape of what they hold.
type node struct {
	kind kind
	doc  string

	enum     []string // allowed values of a kString
	positive bool     // a kInt must be at least 1

	elem *node // the element of a kList

	fields map[string]*node // the known keys of a kTable
	// extra is the shape of every other key of a kTable, which makes the
	// table free-form. Nil means other keys are unknown.
	extra *node
	// oneOf lists keys of a kTable of which exactly one must be set.
	oneOf []string

	// localOnly reports whether a value may only come from config.local.toml
	// or a CLI flag, never from the committed config.toml; why says, in a
	// few words, what it is.
	localOnly func(v any) bool
	why       string
}

func str(doc string) *node               { return &node{kind: kString, doc: doc} }
func enum(doc string, v ...string) *node { return &node{kind: kString, doc: doc, enum: v} }
func boolean(doc string) *node           { return &node{kind: kBool, doc: doc} }
func positiveInt(doc string) *node       { return &node{kind: kInt, doc: doc, positive: true} }
func duration(doc string) *node          { return &node{kind: kDuration, doc: doc} }
func secret(doc string) *node            { return &node{kind: kSecret, doc: doc} }
func listOf(doc string, elem *node) *node {
	return &node{kind: kList, doc: doc, elem: elem}
}
func strList(doc string) *node { return listOf(doc, str("")) }
func table(doc string, fields map[string]*node) *node {
	return &node{kind: kTable, doc: doc, fields: fields}
}
func mapOf(doc string, extra *node) *node {
	return &node{kind: kTable, doc: doc, fields: map[string]*node{}, extra: extra}
}

// local marks n as allowed only outside config.toml when when(v) holds.
func local(n *node, why string, when func(v any) bool) *node {
	n.localOnly, n.why = when, why
	return n
}

func always(any) bool { return true }

// Harnesses and auth methods compiled into this binary.
var (
	harnessNames = []string{"claude_code"}
	authMethods  = []string{"oauth_token", "api_key", "bedrock", "gateway"}
)

// secretSource is the shape of a kSecret: exactly one source.
var secretSource = &node{kind: kTable, oneOf: []string{"env", "command", "stored", "dotenv"}, fields: map[string]*node{
	"env":     str("Read from this environment variable."),
	"command": str("Run this command and use its stdout."),
	"stored":  str("Read this key from the user-level credentials file written by `kraftwerk auth set`."),
	"dotenv":  str("Read this key from .kraftwerk/.env, which must be git-ignored."),
}}

func stepTable(name string) *node {
	return table("Settings of the "+name+" Step.", map[string]*node{
		"enabled":      boolean("Whether the " + name + " Step runs."),
		"harness":      enum("The Harness for this Step. A Step that sets it must also set `model`.", harnessNames...),
		"model":        str("The model for this Step."),
		"effort":       str("The reasoning effort for this Step, if the Harness has one."),
		"idle_timeout": duration("Stop the Step after this long without output. Default 10m."),
		"timeout":      duration("Stop the Step after this long. Default 60m."),
	})
}

func hookPoint(doc string, where ...string) *node {
	entry := table("", map[string]*node{
		"run":      str("A shell command."),
		"parallel": strList("Shell commands run at once; on the first failure the others are stopped."),
		"where":    enum("Where the Hook runs. Default \""+where[0]+"\".", where...),
		"timeout":  duration("Stop the Hook after this long. Default 5m."),
		"secrets":  strList("Project secrets a host Hook gets (Sandbox Hooks get them all)."),
	})
	entry.oneOf = []string{"run", "parallel"}
	return listOf(doc, entry)
}

func harnessTable() *node {
	cred := func(doc string) *node { return local(secret(doc), "a Harness credential", always) }
	return table("Settings of the Claude Code Harness.", map[string]*node{
		"method":            enum("How to sign in.", authMethods...),
		"secret":            cred("Where the token or API key comes from."),
		"base_url":          str("The gateway's URL (method = \"gateway\")."),
		"region":            str("The AWS region (method = \"bedrock\")."),
		"profile":           str("The AWS profile (method = \"bedrock\")."),
		"access_key_id":     cred("Where the AWS access key id comes from (method = \"bedrock\")."),
		"secret_access_key": cred("Where the AWS secret access key comes from (method = \"bedrock\")."),
		"role_arn":          str("A role to assume for Bedrock (method = \"bedrock\")."),
		"i_accept_long_lived_keys_in_sandbox": local(boolean("Hand your own AWS credentials to every Sandbox as they are, instead of short-lived, Bedrock-only ones (method = \"bedrock\"). The agent can read them, and every Run warns."),
			"the acknowledgement for passing AWS credentials through", always),
	})
}

func containsStar(v any) bool {
	list, _ := v.([]any)
	for _, e := range list {
		if e == "*" {
			return true
		}
	}
	return false
}

var schema = table("Kraftwerk configuration.", map[string]*node{
	"version":         {kind: kVersion, doc: "The config format version. Must be 1."},
	"factory":         str("A semver range the Factory's version must match, e.g. \"1\"."),
	"harness":         enum("The Harness every Step uses unless the Step sets its own.", harnessNames...),
	"model":           str("The model every Step uses unless the Step sets its own."),
	"effort":          str("The reasoning effort every Step uses unless the Step sets its own."),
	"max_parallel":    positiveInt("How many Tickets an Iteration works on at once. Default 1."),
	"max_iterations":  positiveInt("How many Iterations a Run may have. Default 10."),
	"verify":          strList("Names from [commands] the Factory runs itself to check a change."),
	"standards":       str("The Coding standards file, read only by the review Step."),
	"protected_paths": strList("Extra Protected paths: files, directories or globs the agent may not change."),

	"tracker": table("Where Tickets live.", map[string]*node{
		"kind":              enum("The Tracker.", "github"),
		"repo":              str("The repository, as owner/name."),
		"ready_label":       str("The label that marks a Ticket Ready for agent."),
		"in_progress_label": str("The label that marks a claimed Spec."),
		"cannot_do_label":   str("The label a Ticket gets when the agent can't do it."),
		"token":             local(secret("Where the Tracker token comes from."), "a Tracker credential", always),
	}),

	"branches": table("Branch names.", map[string]*node{
		"base_branch": str("The remote branch Spec branches are cut from."),
		"prefix":      str("The prefix of every branch the Factory creates."),
	}),

	"sandbox": table("The Sandbox each Step runs in.", map[string]*node{
		"kind": local(enum("The Sandbox kind. \"none\" also needs i_accept_unsandboxed_risk = true.", "container", "native", "none"),
			"kind = \"none\" (no Sandbox)", func(v any) bool { return v == "none" }),
		"i_accept_unsandboxed_risk": local(boolean("Required with kind = \"none\": you accept that the agent runs with no Sandbox."),
			"the acknowledgement for running without a Sandbox", always),
		"run_args":          strList("Extra container engine flags."),
		"read_only_paths":   strList("Extra paths a native Sandbox may read."),
		"writable_paths":    strList("Extra paths a native Sandbox may write."),
		"scrub_credentials": boolean("Keep credentials out of the env of the agent's own commands. Default true."),
		"network": table("Network access from the Sandbox.", map[string]*node{
			"allow": local(strList("Hosts the Sandbox may reach, besides the Harness's own endpoints."),
				"allow = [\"*\"] (open network)", containsStar),
		}),
		"env":     mapOf("Environment variables set in every Sandbox. Not for secrets: use [sandbox.secrets].", str("")),
		"secrets": mapOf("Project secrets, by environment variable name, each naming where it comes from.", secret("")),
	}),

	"network": table("The host's network.", map[string]*node{
		"ca_certs":       strList("PEM files trusted in every Sandbox on top of the system roots."),
		"upstream_proxy": str("The proxy the egress proxy chains to, instead of the host's HTTPS_PROXY."),
	}),

	"services": func() *node {
		n := mapOf("Services from the compose file, by name.", table("A Service.", map[string]*node{
			"shared": boolean("One copy serves the whole Run instead of one per Sandbox."),
			"env":    mapOf("Environment variables with {host} and {port} templates.", str("")),
		}))
		n.fields["compose_file"] = str("The compose file Services come from.")
		return n
	}(),

	"commands": mapOf("Feedback commands, by name.", str("")),

	"hooks": table("Hooks, by the moment they run at.", map[string]*node{
		"run_start":       hookPoint("Runs on the host when a Run starts.", "host"),
		"sandbox_created": hookPoint("Runs when a Sandbox has been created.", "sandbox", "host"),
		"spec_finished":   hookPoint("Runs on the host when a Spec's last Ticket, or a standalone Ticket, closes.", "host"),
		"run_end":         hookPoint("Runs on the host when a Run ends.", "host"),
	}),

	"steps": table("Per-Step settings.", map[string]*node{
		"implement": stepTable("implement"),
		"review":    stepTable("review"),
		"merge":     stepTable("merge"),
	}),

	"harnesses": table("Per-Harness settings, by Harness name.", map[string]*node{
		"claude_code": harnessTable(),
	}),

	"logs": table("Run logs.", map[string]*node{
		"keep_days": {kind: kKeepDays, doc: "How many days Run logs are kept: a positive whole number, or \"forever\"."},
	}),
})
