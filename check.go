package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Marcel-Goehn/kraftwerk/internal/config"
)

// localFlags are the CLI flags that override the config files. They count
// as a local layer, so they may hold local-only keys.
type localFlags struct {
	sandbox     string
	acceptRisk  bool
	allow       []string
	model       string
	maxParallel int
}

// register adds the flags to fs.
func (f *localFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&f.sandbox, "sandbox", "", "Sandbox kind: container, native or none (sandbox.kind)")
	fs.BoolVar(&f.acceptRisk, "i-accept-unsandboxed-risk", false, "allow --sandbox none (sandbox.i_accept_unsandboxed_risk)")
	fs.Func("allow", "a host the Sandbox may reach; repeat for more, \"*\" for any (sandbox.network.allow)", func(s string) error {
		f.allow = append(f.allow, s)
		return nil
	})
	fs.StringVar(&f.model, "model", "", "the model every Step uses (model)")
	fs.IntVar(&f.maxParallel, "max-parallel", 0, "how many Tickets an Iteration works on at once (max_parallel)")
}

// overrides turns the flags that were set into config overrides.
func (f *localFlags) overrides(fs *flag.FlagSet) []config.Override {
	var out []config.Override
	fs.Visit(func(fl *flag.Flag) {
		switch fl.Name {
		case "sandbox":
			out = append(out, config.Override{Key: "sandbox.kind", Value: f.sandbox})
		case "i-accept-unsandboxed-risk":
			out = append(out, config.Override{Key: "sandbox.i_accept_unsandboxed_risk", Value: f.acceptRisk})
		case "allow":
			hosts := make([]any, len(f.allow))
			for i, h := range f.allow {
				hosts[i] = h
			}
			out = append(out, config.Override{Key: "sandbox.network.allow", Value: hosts})
		case "model":
			out = append(out, config.Override{Key: "model", Value: f.model})
		case "max-parallel":
			out = append(out, config.Override{Key: "max_parallel", Value: int64(f.maxParallel)})
		}
	})
	return out
}

// runCheck validates the config and exits 3 if it isn't valid.
func runCheck(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("kraftwerk check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var lf localFlags
	lf.register(fs)
	if err := fs.Parse(args); err != nil {
		return exitConfigError
	}
	if fs.NArg() > 0 {
		_, _ = fmt.Fprintf(stderr, "kraftwerk check: unexpected argument %q\n", fs.Arg(0))
		return exitConfigError
	}
	cwd, err := os.Getwd()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "kraftwerk check: can't find the current directory: %v\n", err)
		return exitConfigError
	}

	r := config.Load(projectRoot(cwd), lf.overrides(fs))
	for _, d := range r.Diagnostics {
		_, _ = fmt.Fprintln(stderr, d.String())
	}
	if !r.OK() {
		_, _ = fmt.Fprintln(stderr, "kraftwerk check: the config is not valid; fix the errors above")
		return exitConfigError
	}
	_, _ = fmt.Fprintln(stdout, "kraftwerk check: the config is valid")
	return exitOK
}

// projectRoot is the nearest directory at or above dir that holds
// .kraftwerk/, looking no further than the enclosing git repository. With
// none found, it is dir itself, where Load then reports the missing config.
func projectRoot(dir string) string {
	for d := dir; ; {
		if isDir(filepath.Join(d, config.Dir)) {
			return d
		}
		if exists(filepath.Join(d, ".git")) {
			return dir
		}
		parent := filepath.Dir(d)
		if parent == d {
			return dir
		}
		d = parent
	}
}

// isDir reports whether p is a directory.
func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// exists reports whether anything is at p.
func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
