package prompt

import (
	"strings"
	"testing"
)

// A template is only ever rendered with its Step's data, so a field it
// names that the data lacks is a typo in the template. It must fail the
// render, which fails the golden tests, instead of rendering as nothing.
func TestATemplateReferencingAMissingFieldFailsToRender(t *testing.T) {
	for name, data := range map[string]any{
		"implement": implementData{},
		"review":    reviewData{},
		"merge":     mergeData{},
	} {
		t.Run(name, func(t *testing.T) {
			broken := parse(name, "# Task\n\nImplement Ticket {{.Ticket.Nmae}}\n")

			_, err := render(broken, data)
			if err == nil || !strings.Contains(err.Error(), "Nmae") {
				t.Errorf("rendering a template with a missing field: err = %v, want one naming the field", err)
			}
		})
	}
}
