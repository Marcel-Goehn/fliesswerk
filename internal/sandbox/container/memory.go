package container

import (
	"context"
	"fmt"
	"math"
	"runtime"
	"strconv"
	"strings"
	"unicode"

	"github.com/Marcel-Goehn/kraftwerk/internal/memory"
)

// statsFormat prints a container's name and "<used> / <limit>".
const statsFormat = "{{.Name}} {{.MemUsage}}"

// statsUnknown is what `stats` prints for a container that is going away.
const statsUnknown = "--"

// sizeUnits are the factors of what `stats` prints: Docker's binary units and
// Podman's decimal ones.
var sizeUnits = map[string]float64{
	"b": 1, "kb": 1e3, "mb": 1e6, "gb": 1e9, "tb": 1e12,
	"kib": 1 << 10, "mib": 1 << 20, "gib": 1 << 30, "tib": 1 << 40,
}

// parseSize reads a size such as 1.5GiB or 512kB.
func parseSize(s string) (int64, error) {
	i := strings.IndexFunc(s, unicode.IsLetter)
	if i <= 0 {
		return 0, fmt.Errorf("reading the size %q", s)
	}
	number, err := strconv.ParseFloat(s[:i], 64)
	if err != nil {
		return 0, fmt.Errorf("reading the size %q: %w", s, err)
	}
	factor, ok := sizeUnits[strings.ToLower(s[i:])]
	if !ok {
		return 0, fmt.Errorf("reading the size %q: unknown unit", s)
	}
	return int64(math.Round(number * factor)), nil
}

// sumStats adds up the memory in `stats` output.
func sumStats(out string) (int64, error) {
	var sum int64
	for line := range strings.Lines(out) {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) < 2 {
			return 0, fmt.Errorf("reading the engine's stats: %q", line)
		}
		if fields[1] == statsUnknown {
			continue
		}
		size, err := parseSize(fields[1])
		if err != nil {
			return 0, err
		}
		sum += size
	}
	return sum, nil
}

// containerMemory is what the named containers hold. No names means nothing:
// `stats` without any would report every container.
func containerMemory(ctx context.Context, e Engine, run cli, names []string) (int64, error) {
	if len(names) == 0 {
		return 0, nil
	}
	out, err := run(ctx, e.argv(append([]string{"stats", "--no-stream", "--format", statsFormat}, names...)...)...)
	if err != nil {
		return 0, err
	}
	return sumStats(out)
}

// engineFree is what the engine has left: its total less what all its
// containers hold, and never more than host says the machine has available,
// where other programs use memory too.
func engineFree(ctx context.Context, e Engine, run cli, host func(context.Context) (int64, error)) (int64, error) {
	format := "{{.MemTotal}}"
	if e.Binary == podman {
		format = "{{.Host.MemTotal}}"
	}
	out, err := run(ctx, e.argv("info", "--format", format)...)
	if err != nil {
		return 0, err
	}
	total, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("reading the engine's memory: %w", err)
	}
	out, err = run(ctx, e.argv("stats", "--no-stream", "--format", statsFormat)...)
	if err != nil {
		return 0, err
	}
	used, err := sumStats(out)
	if err != nil {
		return 0, err
	}
	free := total - used
	if available, err := host(ctx); err == nil {
		free = min(free, available)
	}
	return free, nil
}

// hostLimit is what the machine itself has available, where the engine shares
// the machine's memory. An engine in a VM has its own, so nothing limits it.
func hostLimit(ctx context.Context) (int64, error) {
	if runtime.GOOS != "linux" {
		return 0, memory.ErrUnknown
	}
	return memory.HostAvailable(ctx)
}

// FreeMemory is how many bytes are free for another Sandbox.
func (p *Provider) FreeMemory(ctx context.Context) (int64, error) {
	return engineFree(ctx, p.engine, runCLI, hostLimit)
}

// MemoryUse implements sandbox.Sandbox.
func (b *box) MemoryUse(ctx context.Context) (int64, error) {
	names := append([]string{b.name}, b.ownContainers...)
	if b.forwarder != "" {
		names = append(names, b.forwarder)
	}
	return containerMemory(ctx, b.engine, runCLI, names)
}
