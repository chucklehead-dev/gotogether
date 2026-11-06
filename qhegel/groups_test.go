package qhegel_test

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/chucklehead-dev/gotogether/qhegel"
	"hegel.dev/go/hegel"
)

type groupCoverage struct {
	reads, increments, snapshots atomic.Int64
	readIncrement, readSnapshot  atomic.Bool
}

type groupedBank struct {
	rec      *qhegel.Case
	coverage *groupCoverage
	mu       sync.Mutex
	balance  int
	reading  atomic.Int64
	writing  atomic.Int64
	snapshot atomic.Int64
}

func (m *groupedBank) observeOverlap() {
	runtime.Gosched()
	if m.reading.Load() > 0 {
		if m.writing.Load() > 0 {
			m.coverage.readIncrement.Store(true)
		}
		if m.snapshot.Load() > 0 {
			m.coverage.readSnapshot.Store(true)
		}
	}
}

func (m *groupedBank) RuleRead(tc hegel.TestCase) {
	m.reading.Add(1)
	defer m.reading.Add(-1)
	m.observeOverlap()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.balance < 0 {
		tc.Errorf("negative balance: %d", m.balance)
	}
	m.coverage.reads.Add(1)
}

func (m *groupedBank) RuleIncrement(tc hegel.TestCase) {
	m.writing.Add(1)
	defer m.writing.Add(-1)
	m.observeOverlap()
	if m.snapshot.Load() != 0 {
		tc.Errorf("increment overlapped snapshot")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.balance++
	m.rec.Step("deposit", nil, "account", "alice", "amount", 1,
		"obs.alice", m.balance, "obs.bob", 0)
	m.coverage.increments.Add(1)
}

func (m *groupedBank) RuleSnapshot(tc hegel.TestCase) {
	m.snapshot.Add(1)
	defer m.snapshot.Add(-1)
	m.observeOverlap()
	if m.writing.Load() != 0 {
		tc.Errorf("snapshot overlapped increment")
	}
	m.mu.Lock()
	_ = m.balance
	m.mu.Unlock()
	m.coverage.snapshots.Add(1)
}

// Reads belong to both rounds, while snapshots and mutations remain isolated.
// The mutations still pass through qhegel's recorder and native model check;
// concurrent rule membership must not reorder their observed balances.
func TestSharedRuleGroupsConform(t *testing.T) {
	needQuint(t)
	coverage := &groupCoverage{}
	qhegel.Test(t, qhegel.Config{Binding: "testdata/bank.yaml", Mode: qhegel.Each, AllowEmpty: true},
		func(tc hegel.TestCase, rec *qhegel.Case) {
			hegel.RunStateful(tc, &groupedBank{rec: rec, coverage: coverage},
				hegel.WithBoundedConcurrency(2), hegel.WithStatefulStepCount(20),
				hegel.WithRuleGroup("operations", "RuleRead", "RuleIncrement"),
				hegel.WithRuleGroup("snapshot", "RuleRead", "RuleSnapshot"))
		}, hegel.WithTestCases(20), hegel.WithDatabase(""), hegel.WithSeed(7))
	if coverage.reads.Load() == 0 || coverage.increments.Load() == 0 || coverage.snapshots.Load() == 0 || !coverage.readIncrement.Load() || !coverage.readSnapshot.Load() {
		t.Fatalf("insufficient rule coverage: read=%d increment=%d snapshot=%d read/increment=%t read/snapshot=%t",
			coverage.reads.Load(), coverage.increments.Load(), coverage.snapshots.Load(), coverage.readIncrement.Load(), coverage.readSnapshot.Load())
	}
}
