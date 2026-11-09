package native

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestACACertsFileInAHiddenPlaceIsRefused(t *testing.T) {
	h := Host{Home: "/home/me", RuntimeDir: "/run/user/1000"}
	refused := CheckCACerts([]string{filepath.Join(h.RuntimeDir, "corp.pem"), "/etc/corp.pem"}, h)
	if len(refused) != 1 || !strings.Contains(refused[0], "ca_certs") || !strings.Contains(refused[0], "/run/user/1000/corp.pem") {
		t.Errorf("refused = %q", refused)
	}
}
