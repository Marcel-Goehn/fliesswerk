package native

import "testing"

// psOutput is what `ps -eo pid=,ppid=,pgid=,rss=` prints: a bwrap (100), its
// child (101), that child's children (102, 103), a process that left the tree
// but kept its group (104), and things of nobody's concern.
const psOutput = `    1     0     1  9000
  100    50   100  1000
  101   100   100  2000
  102   101   102  4000
  103   102   102  8000
  104     1   100 16000
  200    50   200 32000
`

func TestAProcessTreeIsAddedUpFromItsRecordedRoots(t *testing.T) {
	got, err := treeMemory(psOutput, map[int]bool{100: true})
	if err != nil {
		t.Fatal(err)
	}
	if want := int64(1000+2000+4000+8000+16000) << 10; got != want {
		t.Errorf("got %d, want %d", got, want)
	}
}

func TestNoRootsHoldNothing(t *testing.T) {
	got, err := treeMemory(psOutput, nil)
	if err != nil || got != 0 {
		t.Errorf("got %d, %v", got, err)
	}
}

func TestOutputItCannotReadIsAnError(t *testing.T) {
	if _, err := treeMemory("nonsense here\n", map[int]bool{1: true}); err == nil {
		t.Error("want an error")
	}
}
