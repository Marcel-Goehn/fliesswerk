package native

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/Marcel-Goehn/kraftwerk/internal/memory"
)

// FreeMemory is how many bytes are free for another Sandbox: a native
// Sandbox's commands run on the machine itself.
func (p *Provider) FreeMemory(ctx context.Context) (int64, error) {
	return memory.HostAvailable(ctx)
}

// MemoryUse implements sandbox.Sandbox: the process trees of its commands, and
// its own Services. The trees are found through the roots its commands record
// when they start, as they run under no parent the Factory could name.
func (b *box) MemoryUse(ctx context.Context) (int64, error) {
	out, err := exec.CommandContext(ctx, "ps", "-eo", "pid=,ppid=,pgid=,rss=").Output()
	if err != nil {
		return 0, fmt.Errorf("listing the processes: %w", err)
	}
	use, err := treeMemory(string(out), recordedRoots(b.groupDir))
	if err != nil {
		return 0, err
	}
	if b.ownServices != nil {
		own, err := b.ownServices.MemoryUse(ctx, b.state)
		if err != nil {
			return 0, err
		}
		use += own
	}
	return use, nil
}

// recordedRoots are the commands' first processes, which are also the leaders
// of their process groups on macOS.
func recordedRoots(dir string) map[int]bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	roots := map[int]bool{}
	for _, e := range entries {
		if pid, err := strconv.Atoi(e.Name()); err == nil {
			roots[pid] = true
		}
	}
	return roots
}

// psProcess is one line of `ps`.
type psProcess struct {
	ppid, pgid int
	rss        int64
}

// treeMemory adds up, from `ps -eo pid=,ppid=,pgid=,rss=` output, the roots,
// what descends from them and what is in their groups. rss is in KiB.
func treeMemory(ps string, roots map[int]bool) (int64, error) {
	processes := map[int]psProcess{}
	for line := range strings.Lines(ps) {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		numbers := make([]int64, 0, len(fields))
		for _, f := range fields {
			n, err := strconv.ParseInt(f, 10, 64)
			if err != nil {
				return 0, fmt.Errorf("reading the process list: %q", line)
			}
			numbers = append(numbers, n)
		}
		if len(numbers) != 4 {
			return 0, fmt.Errorf("reading the process list: %q", line)
		}
		processes[int(numbers[0])] = psProcess{ppid: int(numbers[1]), pgid: int(numbers[2]), rss: numbers[3]}
	}
	inTree := func(pid int) bool {
		for seen := 0; pid > 1 && seen <= len(processes); seen++ {
			if roots[pid] {
				return true
			}
			pid = processes[pid].ppid
		}
		return false
	}
	var kib int64
	for pid, p := range processes {
		if inTree(pid) || roots[p.pgid] {
			kib += p.rss
		}
	}
	return kib << 10, nil
}
