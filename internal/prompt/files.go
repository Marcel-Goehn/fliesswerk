package prompt

import (
	"embed"
	"strings"
)

// The built-in guidance of each Step, builtin/<step>.md.
//
//go:embed builtin
var builtinFS embed.FS

// guidance is every Step's built-in guidance, by Step.
var guidance = loadGuidance()

// loadGuidance reads every Step's guidance, with "\n" line ends however
// git checked it out.
func loadGuidance() map[string]string {
	out := map[string]string{}
	for _, step := range Steps {
		text, err := builtinFS.ReadFile("builtin/" + step + ".md")
		if err != nil {
			panic(err) // the guidance is compiled in
		}
		out[step] = strings.ReplaceAll(string(text), "\r\n", "\n")
	}
	return out
}
