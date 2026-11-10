package memory_test

import (
	"testing"

	"github.com/Marcel-Goehn/kraftwerk/internal/memory"
)

const meminfo = `MemTotal:       32562612 kB
MemFree:          812344 kB
MemAvailable:   20480000 kB
Buffers:          428120 kB
`

const vmStat = `Mach Virtual Memory Statistics: (page size of 16384 bytes)
Pages free:                               10000.
Pages active:                            400000.
Pages inactive:                           20000.
Pages speculative:                         3000.
Pages throttled:                              0.
Pages wired down:                        150000.
Pages purgeable:                           1000.
`

func TestMeminfoGivesWhatTheHostHasAvailable(t *testing.T) {
	got, err := memory.ParseMeminfo(meminfo)
	if err != nil {
		t.Fatal(err)
	}
	if want := int64(20480000) * 1024; got != want {
		t.Errorf("got %d, want %d", got, want)
	}
}

func TestMeminfoWithoutMemAvailableIsAnError(t *testing.T) {
	if _, err := memory.ParseMeminfo("MemTotal: 1 kB\n"); err == nil {
		t.Error("want an error")
	}
}

func TestVMStatCountsThePagesTheSystemCanGiveUp(t *testing.T) {
	got, err := memory.ParseVMStat(vmStat)
	if err != nil {
		t.Fatal(err)
	}
	if want := int64(10000+20000+3000+1000) * 16384; got != want {
		t.Errorf("got %d, want %d", got, want)
	}
}
