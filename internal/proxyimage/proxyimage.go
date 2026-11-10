// Package proxyimage holds what the proxy sidecar's image is made of: for
// each architecture, a root filesystem with only the static egress proxy in
// it, ready for `docker|podman import`. `go generate` makes them; a build
// without that step has none, and says so when a Run needs one.
package proxyimage

import (
	"embed"
	"fmt"
)

//go:generate go run ./gen

// Arches are the architectures a release has the proxy for.
var Arches = []string{"amd64", "arm64"}

// Entrypoint is where the proxy is in the image.
const Entrypoint = "/egress-proxy"

//go:embed dist
var dist embed.FS

// FileName is the name of the root filesystem for arch.
func FileName(arch string) string { return "rootfs-" + arch + ".tar.gz" }

// Rootfs is the gzipped root filesystem for arch.
func Rootfs(arch string) ([]byte, error) {
	data, err := dist.ReadFile("dist/" + FileName(arch))
	if err != nil {
		return nil, fmt.Errorf("this fliesswerk was built without the egress proxy for linux/%s, so it can't run container Sandboxes; install a release, or run `go generate ./internal/proxyimage` in its source before building", arch)
	}
	return data, nil
}
