package memory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

// ErrUnknown says the host's free memory can't be read on this system.
var ErrUnknown = errors.New("this system's free memory can't be read")

// HostAvailable is how many bytes the host can give a new program.
func HostAvailable(ctx context.Context) (int64, error) {
	switch runtime.GOOS {
	case "linux":
		data, err := os.ReadFile("/proc/meminfo")
		if err != nil {
			return 0, err
		}
		return ParseMeminfo(string(data))
	case "darwin":
		out, err := exec.CommandContext(ctx, "vm_stat").Output()
		if err != nil {
			return 0, fmt.Errorf("vm_stat: %w", err)
		}
		return ParseVMStat(string(out))
	}
	return 0, ErrUnknown
}

// ParseMeminfo reads MemAvailable from /proc/meminfo.
func ParseMeminfo(text string) (int64, error) {
	for line := range strings.Lines(text) {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "MemAvailable:" {
			kb, err := strconv.ParseInt(fields[1], 10, 64)
			if err != nil {
				return 0, fmt.Errorf("reading MemAvailable: %w", err)
			}
			return kb << 10, nil
		}
	}
	return 0, errors.New("/proc/meminfo has no MemAvailable")
}

// pageSizeLine finds the page size in `vm_stat`'s first line.
var pageSizeLine = regexp.MustCompile(`page size of (\d+) bytes`)

// ParseVMStat reads macOS's `vm_stat`: the pages that are free or that the
// system gives up without writing them out.
func ParseVMStat(text string) (int64, error) {
	match := pageSizeLine.FindStringSubmatch(text)
	if match == nil {
		return 0, errors.New("vm_stat gives no page size")
	}
	size, err := strconv.ParseInt(match[1], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("reading vm_stat's page size: %w", err)
	}
	var pages int64
	found := false
	for line := range strings.Lines(text) {
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch name {
		case "Pages free", "Pages inactive", "Pages speculative", "Pages purgeable":
			n, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimSpace(value), "."), 10, 64)
			if err != nil {
				return 0, fmt.Errorf("reading vm_stat's %s: %w", name, err)
			}
			pages += n
			found = true
		}
	}
	if !found {
		return 0, errors.New("vm_stat gives no page counts")
	}
	return pages * size, nil
}
