package history

import (
	"strings"
	"testing"
	"time"
)

// commit adds t and returns the violation's phenomenon, or "".
func commit(t *testing.T, h *History, tx Txn) string {
	t.Helper()
	if err := h.Commit(tx); err != nil {
		return err.Error()
	}
	return ""
}

// Two increments that read the same version and both write: a lost
// update, G-single, which only read committed allows.
func TestLostUpdate(t *testing.T) {
	for _, c := range []struct {
		level int
		want  string
	}{{ReadCommitted, ""}, {SnapshotIsolation, "G-single"}, {Serializable, "G-single"}} {
		h := New(c.level)
		h.Load("n", 1)
		commit(t, h, Txn{ID: 1, Commit: 2, Reads: []Read{{"n", 1}}, Writes: []string{"n"}})
		got := commit(t, h, Txn{ID: 2, Commit: 3, Reads: []Read{{"n", 1}}, Writes: []string{"n"}})
		if !strings.HasPrefix(got, c.want) || (c.want == "") != (got == "") {
			t.Errorf("level %d: %q, want %q", c.level, got, c.want)
		}
	}
}

// Each reads both doctors and takes itself off call: write skew, G2 with
// two rw edges, which snapshot isolation allows.
func TestWriteSkew(t *testing.T) {
	for _, c := range []struct {
		level int
		want  string
	}{{ReadCommitted, ""}, {SnapshotIsolation, ""}, {Serializable, "G2"}} {
		h := New(c.level)
		h.Load("a", 1)
		h.Load("b", 2)
		commit(t, h, Txn{ID: 1, Commit: 3, Reads: []Read{{"a", 1}, {"b", 2}}, Writes: []string{"a"}})
		got := commit(t, h, Txn{ID: 2, Commit: 4, Reads: []Read{{"a", 1}, {"b", 2}}, Writes: []string{"b"}})
		if !strings.HasPrefix(got, c.want) || (c.want == "") != (got == "") {
			t.Errorf("level %d: %q, want %q", c.level, got, c.want)
		}
	}
}

// A read of a version no committed transaction installed (another's
// uncommitted write) is a dirty read, which no level allows.
func TestDirtyRead(t *testing.T) {
	h := New(ReadCommitted)
	if got := commit(t, h, Txn{ID: 1, Commit: 1, Reads: []Read{{"y", 2}}, Writes: []string{"x"}}); !strings.HasPrefix(got, "G1a") {
		t.Errorf("%q, want G1a", got)
	}
}

// A scan that missed a key inserted after its view, by a transaction the
// scanner's own write then follows: a phantom, G2 through a predicate
// anti-dependency.
func TestPhantom(t *testing.T) {
	h := New(Serializable)
	h.Load("cfg", 1)
	// T1 scans job/ (empty at version 1) and writes cfg
	// T2 reads cfg's version 1 and inserts job/1
	commit(t, h, Txn{ID: 1, Commit: 2, Scans: []Scan{{"job/", 1}}, Writes: []string{"cfg"}})
	got := commit(t, h, Txn{ID: 2, Commit: 3, Reads: []Read{{"cfg", 1}}, Writes: []string{"job/1"}})
	if !strings.HasPrefix(got, "G2") {
		t.Errorf("%q, want G2", got)
	}
	if h2 := New(SnapshotIsolation); true {
		h2.Load("cfg", 1)
		commit(t, h2, Txn{ID: 1, Commit: 2, Scans: []Scan{{"job/", 1}}, Writes: []string{"cfg"}})
		if got := commit(t, h2, Txn{ID: 2, Commit: 3, Reads: []Read{{"cfg", 1}}, Writes: []string{"job/1"}}); got != "" {
			t.Errorf("snapshot isolation: %q, want none", got)
		}
	}
}

// A serial history has no cycle at any level.
func TestSerial(t *testing.T) {
	h := New(Serializable)
	h.Load("n", 1)
	for i := int64(1); i <= 20; i++ {
		if got := commit(t, h, Txn{ID: i, Commit: Version(i + 1), Reads: []Read{{"n", Version(i)}}, Writes: []string{"n"}}); got != "" {
			t.Fatalf("step %d: %s", i, got)
		}
	}
}

// Two read-only transactions, each seeing one writer's effect but not
// the other's: 3 -rw(z)-> 4 -wr(w)-> 1 -rw(x)-> 2 -wr(y)-> 3, two rw
// edges never in a row, G-nonadjacent, which snapshot isolation forbids
// (no snapshot shows it) though it allows write skew. A search for the
// cycle with the fewest rw edges took it for write skew.
func TestNonadjacent(t *testing.T) {
	for _, c := range []struct {
		level int
		want  string
	}{{ReadCommitted, ""}, {SnapshotIsolation, "G-nonadjacent"}, {Serializable, "G-nonadjacent"}} {
		h := New(c.level)
		for i, k := range []string{"x", "y", "z", "w"} {
			h.Load(k, Version(i+1))
		}
		commit(t, h, Txn{ID: 2, Commit: 5, Writes: []string{"x", "y"}})
		commit(t, h, Txn{ID: 4, Commit: 6, Writes: []string{"z", "w"}})
		commit(t, h, Txn{ID: 1, Reads: []Read{{"x", 1}, {"w", 6}}})
		got := commit(t, h, Txn{ID: 3, Reads: []Read{{"y", 5}, {"z", 3}}})
		if !strings.HasPrefix(got, c.want) || (c.want == "") != (got == "") {
			t.Errorf("level %d: %q, want %q", c.level, got, c.want)
		}
		if c.want != "" && !strings.HasPrefix(got, c.want+" (anti-dependencies, never two in a row): T3 -rw(z)-> T4") {
			t.Errorf("level %d: %q, want the cycle from T3, which closed it", c.level, got)
		}
	}
}

// A hot counter at read committed, each transaction reading the version
// of two commits back (lost updates, which the level allows): the allowed
// cycles make one component of the whole history, and a commit classifies
// only the cycles through it, from it, so 2000 commits take milliseconds
// (classifying the component took 2.9 s for 200).
func TestCostOfAllowedCycles(t *testing.T) {
	h := New(ReadCommitted)
	h.Load("c", 1)
	vers := []Version{1}
	start := time.Now()
	for i := range 2000 {
		v := Version(i + 2)
		r := vers[max(0, len(vers)-3)]
		if err := h.Commit(Txn{ID: int64(i + 1), Commit: v, Reads: []Read{{"c", r}}, Writes: []string{"c"}}); err != nil {
			t.Fatalf("commit %d: %v", i+1, err)
		}
		vers = append(vers, v)
	}
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Errorf("2000 commits took %v", d)
	}
	t.Logf("2000 commits: %v", time.Since(start))
}
