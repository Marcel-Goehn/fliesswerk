//go:build linux

package native

import (
	"context"
	"errors"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// machine fakes what Check looks at.
func machine(probeOutput string, probeErr error, sysctls map[string]string) prerequisites {
	return prerequisites{
		lookPath: func(string) (string, error) { return "/usr/bin/bwrap", nil },
		probe:    func(context.Context, string) (string, error) { return probeOutput, probeErr },
		sysctl:   func(name string) string { return sysctls[name] },
	}
}

func TestAMachineWhereBwrapWorksPasses(t *testing.T) {
	if _, err := machine("", nil, map[string]string{"kernel/apparmor_restrict_unprivileged_userns": "1"}).check(context.Background()); err != nil {
		t.Fatalf("a working probe is what counts: %v", err)
	}
}

func TestMissingBwrapNamesTheInstallCommand(t *testing.T) {
	m := machine("", nil, nil)
	m.lookPath = func(string) (string, error) { return "", exec.ErrNotFound }
	_, err := m.check(context.Background())
	if err == nil || !strings.Contains(err.Error(), "apt install bubblewrap") {
		t.Fatalf("err = %v", err)
	}
}

func TestProbeFailuresNameTheFix(t *testing.T) {
	failed := errors.New("exit status 1")
	for name, tt := range map[string]struct {
		output  string
		sysctls map[string]string
		says    []string
	}{
		"AppArmor on Ubuntu 24.04": {
			output:  "bwrap: setting up uid map: Permission denied",
			sysctls: map[string]string{"kernel/apparmor_restrict_unprivileged_userns": "1"},
			says:    []string{"AppArmor", "/etc/apparmor.d/bwrap", "profile bwrap /usr/bin/bwrap", "userns,", "apparmor_parser -r"},
		},
		"Debian's switch": {
			output:  "bwrap: No permissions to create new namespace",
			sysctls: map[string]string{"kernel/unprivileged_userns_clone": "0"},
			says:    []string{"sudo sysctl -w kernel.unprivileged_userns_clone=1"},
		},
		"no user namespaces allowed": {
			output:  "bwrap: No permissions to create new namespace",
			sysctls: map[string]string{"user/max_user_namespaces": "0"},
			says:    []string{"sudo sysctl -w user.max_user_namespaces=15000"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := machine(tt.output, failed, tt.sysctls).check(context.Background())
			var blocked *BlockedError
			if !errors.As(err, &blocked) {
				t.Fatalf("err = %v, want a BlockedError", err)
			}
			for _, s := range tt.says {
				if !strings.Contains(err.Error(), s) {
					t.Errorf("doesn't say %q:\n%v", s, err)
				}
			}
		})
	}
}

func TestAnUnrecognisedFailureShowsWhatBwrapSaid(t *testing.T) {
	_, err := machine("bwrap: something new", errors.New("exit status 1"), nil).check(context.Background())
	if err == nil || !strings.Contains(err.Error(), "bwrap: something new") {
		t.Fatalf("err = %v", err)
	}
}

func TestTheAppArmorFixWorksWhenPasted(t *testing.T) {
	_, err := machine("denied", errors.New("exit status 1"), map[string]string{"kernel/apparmor_restrict_unprivileged_userns": "1"}).check(context.Background())
	if err == nil {
		t.Fatal("no error")
	}
	if !slices.Contains(strings.Split(err.Error(), "\n"), "EOF") {
		t.Errorf("the heredoc's terminator must stand alone on its line:\n%v", err)
	}
}
