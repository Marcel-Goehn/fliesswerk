package container

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// recorded answers engine calls with what the engine printed.
func recorded(answers map[string]string) cli {
	return func(_ context.Context, argv ...string) (string, error) {
		line := strings.Join(argv, " ")
		for part, out := range answers {
			if strings.Contains(line, part) {
				return out, nil
			}
		}
		return "", errors.New("unexpected call: " + line)
	}
}

func noHost(context.Context) (int64, error) { return 0, errors.New("not on this machine") }

func TestContainerMemoryAddsUpEachEnginesUnits(t *testing.T) {
	for name, c := range map[string]struct {
		engine Engine
		stats  string
		want   int64
	}{
		"docker": {
			Engine{Binary: docker},
			"kraftwerk-r-build-2 1.5GiB / 15.52GiB\nkraftwerk-r-build-2-forward 512KiB / 15.52GiB\n",
			3<<29 + 512<<10,
		},
		"podman": {
			Engine{Binary: podman},
			"kraftwerk-r-build-2 1.5GB / 16.6GB\nkraftwerk-r-build-2-forward 512kB / 16.6GB\n",
			1_500_000_000 + 512_000,
		},
		"a stopped container":    {Engine{Binary: docker}, "gone 0B / 0B\n", 0},
		"a container going away": {Engine{Binary: docker}, "here 1MiB / 8GiB\ngoing -- / --\n", 1 << 20},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := containerMemory(context.Background(), c.engine, recorded(map[string]string{" stats ": c.stats}), []string{"x"})
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Errorf("got %d, want %d", got, c.want)
			}
		})
	}
}

func TestContainerMemoryRefusesOutputItCannotRead(t *testing.T) {
	if _, err := containerMemory(context.Background(), Engine{Binary: docker}, recorded(map[string]string{" stats ": "x lots / more\n"}), []string{"x"}); err == nil {
		t.Error("want an error")
	}
}

func TestContainerMemoryOfNothingAsksNothing(t *testing.T) {
	got, err := containerMemory(context.Background(), Engine{Binary: docker}, recorded(nil), nil)
	if err != nil || got != 0 {
		t.Errorf("got %d, %v", got, err)
	}
}

func TestFreeMemoryIsTheEnginesTotalLessWhatItsContainersHold(t *testing.T) {
	for name, c := range map[string]struct {
		engine  Engine
		answers map[string]string
	}{
		"docker": {Engine{Binary: docker}, map[string]string{"{{.MemTotal}}": "8589934592\n", " stats ": "a 1GiB / 8GiB\nb 1GiB / 8GiB\n"}},
		"podman": {Engine{Binary: podman}, map[string]string{"{{.Host.MemTotal}}": "8589934592\n", " stats ": "a 1GiB / 8GiB\nb 1GiB / 8GiB\n"}},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := engineFree(context.Background(), c.engine, recorded(c.answers), noHost)
			if err != nil {
				t.Fatal(err)
			}
			if want := int64(6) << 30; got != want {
				t.Errorf("got %d, want %d", got, want)
			}
		})
	}
}

func TestFreeMemoryIsNoMoreThanTheHostHasAvailable(t *testing.T) {
	answers := map[string]string{"{{.MemTotal}}": "8589934592\n", " stats ": ""}
	got, err := engineFree(context.Background(), Engine{Binary: docker}, recorded(answers), func(context.Context) (int64, error) { return 1 << 30, nil })
	if err != nil {
		t.Fatal(err)
	}
	if want := int64(1) << 30; got != want {
		t.Errorf("got %d, want %d: other programs on the machine use memory too", got, want)
	}
}
