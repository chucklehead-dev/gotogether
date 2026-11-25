package qweave_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qweave"
)

// A //quint:guards field's Unlocks (a call, a deferred call on a local
// value, a method value of a read-write lock) are woven into the guarded
// unlock, the struct bound once and the invariants bound to it; the same
// code without the directive keeps the plain woven Unlock, and RUnlock
// stays unchecked. TestWovenFixtureRuns runs them.
func TestWeaveGuardedFields(t *testing.T) {
	r, err := qweave.Weave(qweave.Config{Dir: "testdata/sync"})
	if err != nil {
		t.Fatal(err)
	}
	var src string
	for name, b := range r.Files {
		if strings.HasSuffix(name, filepath.Join("guarded", "fields.go")) {
			src = string(b)
		}
	}
	for _, want := range []string{
		`quintwoven.MutexUnlockGuarded(func() quintwoven.GuardedMutex { __qw_g := p; return quintwoven.GuardedMutex{Mutex: &__qw_g.mu, Invariants: []quintwoven.Invariant{{Name: "Purse.ledgered", Site: "fields.go:17", Holds: func() bool { return __qw_g.ledgered() }}}} }()) // the release`,
		`defer quintwoven.MutexUnlockGuarded(func() quintwoven.GuardedMutex { __qw_g := &(p); return`, // a local value: its address, at the defer
		`return func() func() { __qw_g := t; return func() { quintwoven.RWUnlockGuarded(quintwoven.GuardedRWMutex{RWMutex: &__qw_g.mu, Invariants: []quintwoven.Invariant{{Name: "Tally.counted", Site: "fields.go:110", Holds: func() bool { return __qw_g.counted() }}}}) } }()`,
		`defer woven.RUnlock(&t.mu)`,
		"\tquintwoven \"github.com/chucklehead-dev/gotogether/woven\"\n",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("woven fields.go lacks %s:\n%s", want, src)
		}
	}
	// PlainPurse's Unlock: the plain one
	if strings.Count(src, "MutexUnlockGuarded(") != 2 || !strings.Contains(src, "woven.MutexUnlock(&p.mu)\n") {
		t.Errorf("want two guarded Unlocks (Purse's, DepositOnce's) and PlainPurse's plain:\n%s", src)
	}
	if len(r.Skipped) != 0 {
		t.Errorf("skipped: %v", r.Skipped)
	}
}

// What a call site cannot see is refused, each with the fix: a guarded
// field's address taken or the lock copied, an Unlock in another package
// whose invariant is unexported, a directive on an embedded or pointer
// lock, naming no method, a method of another type, or a key:value; a build
// without qenv/weave's Unlocks (gosim's tool file) checks nothing and says
// so for each field.
func TestWeaveGuardsRefusals(t *testing.T) {
	for _, c := range []struct{ pkg, want string }{
		{"escape", "escape.go:15:46: Box.mu used other than as its methods' receiver (its address taken, or the lock copied): an Unlock through it is not woven with the invariants //quint:guards names at "},
		{"escape", "or drop the directive and guard it with qenv.Guard(ctx, &x.mu, name, holds), which sees every simulated Unlock"},
		{"cross/user", "Box.Mu's Unlock outside its package example.com/guards/cross: the invariant full, which //quint:guards names at "},
		{"embedded", "//quint:guards on an embedded lock: the struct itself is then a sync.Locker, whose Unlocks a call site cannot see; name the field"},
		{"pointer", "//quint:guards on a field of type *sync.Mutex: it guards a sync.Mutex or sync.RWMutex field held by value"},
		{"nomethod", "//quint:guards full: Box has no method full; declare func (x *Box) full() bool"},
		{"badsig", "//quint:guards full: Box.full is func(n int) bool, want func() bool"},
		{"keyed", `each argument is the name of a method of Box (func() bool), not "name:full"`},
		{"empty", "//quint:guards names no invariant: name a method of Box"},
	} {
		_, err := qweave.Weave(qweave.Config{Dir: "testdata/guards", Patterns: []string{"./" + c.pkg}})
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v\nwant %s", c.pkg, err, c.want)
		}
	}
	r, err := qweave.Weave(qweave.Config{Dir: "testdata/guards", GOSIM: true, Patterns: []string{"./escape"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Skipped) != 1 || !strings.Contains(r.Skipped[0], "escape.go:9:16: //quint:guards on Box.mu not checked: the tool file's aspects weave no sync Unlock (qenv/weave is not named; gosim simulates sync itself)") {
		t.Errorf("without qenv/weave: %v", r.Skipped)
	}
}

// A core whose package woven predates the directive (its ABI1 without
// MutexUnlockGuarded) is refused before weaving a guarded Unlock, naming
// the core and the fix; the same core weaves code without the directive.
func TestWeaveGuardsRefusesOlderCore(t *testing.T) {
	_, err := qweave.Weave(qweave.Config{Dir: "testdata/oldguards", Patterns: []string{"./guarded"}})
	if err == nil || !strings.Contains(err.Error(), "qweave: //quint:guards fields' Unlocks call github.com/chucklehead-dev/gotogether/woven.MutexUnlockGuarded, which the module's core (github.com/chucklehead-dev/gotogether v0.0.0 => ./core) lacks") ||
		!strings.Contains(err.Error(), "go get github.com/chucklehead-dev/gotogether@latest") {
		t.Errorf("older core: %v", err)
	}
	r, err := qweave.Weave(qweave.Config{Dir: "testdata/oldguards", Patterns: []string{"./plain"}})
	if err != nil || len(r.Files) != 1 {
		t.Errorf("older core, no directive: %v %v", err, r)
	}
}
