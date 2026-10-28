package qtest

import (
	"bytes"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
)

// lockedBuffer is a buffer written from the progress goroutine.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}
func (l *lockedBuffer) Close() error { return nil }
func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// A long exploration says how far it got on the terminal, every
// progressEvery: go test holds back a package's output until it ends, and
// a grist app's first suite sat silent for six minutes.
func TestExplorationProgress(t *testing.T) {
	RequireQuint(t)
	out := &lockedBuffer{}
	every, open := progressEvery, progressOut
	progressEvery, progressOut = 20*time.Millisecond, func() (io.WriteCloser, error) { return out, nil }
	defer func() { progressEvery, progressOut = every, open }()
	sys := System{Binding: "../validate/testdata/account.yaml", Run: func(c qenv.Chooser) error {
		time.Sleep(15 * time.Millisecond) //qenv:ok a slow run, in real time, for the progress line
		s := qenv.New(c)
		defer s.Close()
		qobs.Record(s.Context(), "deposit", nil, "amount", 10, "obs.balance", 10)
		return s.Run(0)
	}}
	sys.Explore(t, ExploreConfig{Seeds: 8, Parallel: 1}).Expect(t, Conforms())
	if s := out.String(); !strings.Contains(s, "gotogether: TestExplorationProgress#1: ") || !strings.Contains(s, " of 8 runs made, ") {
		t.Errorf("progress:\n%s", s)
	}
}

// Under -short a short exploration's seeds are topped up, a batch at a
// time, while a fault kind its Rates set was reached and faulted by no
// run, up to its full Seeds; a kind every run faults, or one the Rates
// leave at their defaults, adds none.
func TestShortTopUp(t *testing.T) {
	fault := func(kind string, v int) *Run {
		return &Run{Choices: []qenv.Choice{{Kind: "fault." + kind, N: 3, Value: v}}}
	}
	if got := setKinds(qenv.DefaultRates.Set("store.orders.commit", 0.15).Set("rpc.pay", 0)); len(got) != 1 || got[0] != "store.orders.commit" {
		t.Errorf("setKinds: %v, want the commit's alone (nemesis at its default, rpc.pay at 0)", got)
	}
	kinds := []string{"store.orders"}
	if !unfaultedKind([]*Run{fault("store.orders.commit", 0), fault("rpc.pay.request", 1)}, kinds) {
		t.Error("the commit reached and never faulted: want more seeds")
	}
	if unfaultedKind([]*Run{fault("store.orders.commit", 0), fault("store.orders.commit", 2)}, kinds) {
		t.Error("the commit faulted: no more seeds")
	}
	if unfaultedKind([]*Run{fault("rpc.pay.request", 0)}, kinds) {
		t.Error("the commit never reached: nothing to top up for")
	}
	s := &topUpSource{from: 4, to: 9, batch: 4, kinds: kinds, chooser: func(seed uint64) qenv.Chooser { return qenv.NewSeeded(seed) }}
	runs := []*Run{fault("store.orders.commit", 0)}
	if b := s.next(runs); len(b) != 4 || b[0].origin.seed != 4 || b[3].origin.seed != 7 {
		t.Errorf("first batch: %+v", b)
	}
	if b := s.next(runs); len(b) != 2 || b[1].origin.seed != 9 {
		t.Errorf("second batch, to the full seeds: %+v", b)
	}
	if b := s.next(runs); b != nil {
		t.Errorf("past the full seeds: %+v", b)
	}
	s = &topUpSource{from: 4, to: 9, batch: 4, kinds: kinds}
	if b := s.next([]*Run{fault("store.orders.commit", 1)}); b != nil {
		t.Errorf("faulted already: %+v", b)
	}
}
