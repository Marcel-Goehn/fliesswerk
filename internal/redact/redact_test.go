package redact_test

import (
	"strings"
	"testing"

	"github.com/Marcel-Goehn/kraftwerk/internal/redact"
)

const secret = "sk-ant-oat01-a+b/c=d?e&f g" //nolint:gosec // a made-up secret

func TestStringReplacesEveryFormOfASecret(t *testing.T) {
	r := redact.New(secret)

	tests := []struct{ name, in string }{
		{"raw", "token " + secret + " used"},
		// Worked out by hand from the secret above.
		{"base64", "auth c2stYW50LW9hdDAxLWErYi9jPWQ/ZSZmIGc= sent"},
		{"base64 URL alphabet", "auth c2stYW50LW9hdDAxLWErYi9jPWQ_ZSZmIGc= sent"},
		{"base64 unpadded", "auth c2stYW50LW9hdDAxLWErYi9jPWQ/ZSZmIGc sent"},
		{"query escaped", "https://x/?t=sk-ant-oat01-a%2Bb%2Fc%3Dd%3Fe%26f+g done"},
		{"path escaped", "https://x/sk-ant-oat01-a+b%2Fc=d%3Fe&f%20g done"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := r.String(tt.in)
			if !strings.Contains(got, "***") || strings.Contains(got, "sk-ant") || strings.Contains(got, "c2stYW50") {
				t.Errorf("String(%q) = %q, want the secret replaced with ***", tt.in, got)
			}
		})
	}
}

func TestStringLeavesOtherTextAlone(t *testing.T) {
	r := redact.New(secret, "")
	in := "nothing secret here: sk-ant-other"
	if got := r.String(in); got != in {
		t.Errorf("String(%q) = %q, want it unchanged", in, got)
	}
}

func TestSecretsAddedLaterAreRedactedToo(t *testing.T) {
	r := redact.New()
	r.Add("hunter2-hunter2")
	if got := r.String("pw=hunter2-hunter2."); got != "pw=***." {
		t.Errorf("String = %q, want pw=***.", got)
	}
}

func TestALongerSecretWinsOverOneItContains(t *testing.T) {
	r := redact.New("abcd1234", "abcd1234-and-more")
	if got := r.String("x abcd1234-and-more y"); got != "x *** y" {
		t.Errorf("String = %q, want one ***", got)
	}
}

func TestWriterRedactsWhatPassesThrough(t *testing.T) {
	var b strings.Builder
	w := redact.New(secret).Writer(&b)

	n, err := w.Write([]byte("line with " + secret + "\n"))

	if err != nil || n != len("line with "+secret+"\n") {
		t.Errorf("Write = %d, %v; want the whole input reported written", n, err)
	}
	if b.String() != "line with ***\n" {
		t.Errorf("wrote %q, want the secret redacted", b.String())
	}
}

func TestLeaks(t *testing.T) {
	r := redact.New(secret)
	if !r.Leaks("x " + secret) {
		t.Error("Leaks missed the raw secret")
	}
	if r.Leaks("x *** y") {
		t.Error("Leaks found a secret in redacted text")
	}
}

func TestAJSONEscapedSecretIsRedactedToo(t *testing.T) {
	r := redact.New(`pa"ss\word<1>`)
	line := `{"command":"login pa\"ss\\word<1>"}`
	if got, want := r.String(line), `{"command":"login ***"}`; got != want {
		t.Errorf("String(%q) = %q, want %q", line, got, want)
	}
}
