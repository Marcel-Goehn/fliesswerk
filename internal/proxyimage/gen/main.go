// Command gen builds the egress proxy for every architecture a release
// supports and writes each as a root filesystem into dist/. It runs from
// internal/proxyimage, through `go generate`.
package main

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Marcel-Goehn/kraftwerk/internal/proxyimage"
)

const proxyPackage = "github.com/Marcel-Goehn/kraftwerk/internal/egress/proxycmd"

func main() {
	for _, arch := range proxyimage.Arches {
		if err := generate(arch); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "linux/%s: %v\n", arch, err)
			os.Exit(1)
		}
	}
}

func generate(arch string) error {
	dir, err := os.MkdirTemp("", "kraftwerk-proxy")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	bin := filepath.Join(dir, "proxy")
	cmd := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-ldflags=-s -w", "-o", bin, proxyPackage) //nolint:gosec // fixed arguments and our own temp dir
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+arch)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("go build: %w\n%s", err, out)
	}
	return writeRootfs(bin, filepath.Join("dist", proxyimage.FileName(arch)))
}

// writeRootfs writes a gzipped tar holding only the proxy. It has no
// timestamps, so the same proxy always gives the same file.
func writeRootfs(bin, target string) (err error) {
	data, err := os.ReadFile(bin) //nolint:gosec // the file generate just built
	if err != nil {
		return err
	}
	f, err := os.Create(target) //nolint:gosec // a fixed name in dist/
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}()
	gz, err := gzip.NewWriterLevel(f, gzip.BestCompression)
	if err != nil {
		return err
	}
	tw := tar.NewWriter(gz)
	header := &tar.Header{Name: strings.TrimPrefix(proxyimage.Entrypoint, "/"), Mode: 0o755, Size: int64(len(data))}
	if err := tw.WriteHeader(header); err != nil {
		return err
	}
	if _, err := tw.Write(data); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	return gz.Close()
}
