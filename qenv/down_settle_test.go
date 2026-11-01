package qenv_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
)

func downSettleSim(t *testing.T) (*qenv.Sim, *qobs.MemorySink) {
	t.Helper()
	sink := &qobs.MemorySink{}
	rec := qobs.NewRecorder(sink)
	s := qenv.New(qenv.WithContext(qenv.Nominal{}, qobs.WithRecorder(t.Context(), rec)))
	t.Cleanup(s.Close)
	return s, sink
}

func requireSettleRecords(t *testing.T, sink *qobs.MemorySink, want int) {
	t.Helper()
	got := 0
	for _, step := range sink.Steps() {
		if step.Settled {
			got++
		}
	}
	if got != want {
		t.Fatalf("settle markers = %d, want %d: %+v", got, want, sink.Steps())
	}
}

// A killed main task is not pending work, but its absent process cannot certify
// whole-system recovery. Returning normally from main remains permitted.
func TestSettleRequiresDownProcessesToRecover(t *testing.T) {
	s, sink := downSettleSim(t)
	p := s.Process("worker", false, func(context.Context) {})
	if err := s.Settle(time.Second); err != nil {
		t.Fatalf("completed main task should settle: %v", err)
	}
	p.Crash()
	err := s.Settle(time.Second)
	var u *qenv.Unsettled
	if !errors.As(err, &u) || !errors.Is(err, qenv.ErrUnsettled) {
		t.Fatalf("down process certified settlement: %v", err)
	}
	if len(u.Tasks) != 0 || !slices.Equal(u.DownProcesses, []qenv.ProcessIdentity{{Name: "worker", Incarnation: 1, Index: 1}}) {
		t.Fatalf("availability confused with killed task work: %+v", u)
	}
	if !strings.Contains(err.Error(), "worker#1") || !strings.Contains(err.Error(), "SettleCore") {
		t.Fatalf("error omits identity or recovery action: %v", err)
	}
	requireSettleRecords(t, sink, 1)
	p.Restart()
	if err := s.Settle(time.Second); err != nil {
		t.Fatal(err)
	}
	if u.DownProcesses[0].Incarnation != 1 || p.Incarnation != 2 {
		t.Fatal("restart mutated captured down identity")
	}
	requireSettleRecords(t, sink, 2)
}

func TestSettlePermitsRecoveryAtTheBound(t *testing.T) {
	for _, recovery := range []time.Duration{time.Second / 2, time.Second, 2 * time.Second} {
		t.Run(recovery.String(), func(t *testing.T) {
			s, sink := downSettleSim(t)
			p := s.Process("worker", false, func(context.Context) {})
			p.Crash()
			s.Spawn("supervisor", func(ctx context.Context) {
				qenv.Sleep(ctx, recovery)
				p.Restart()
			})
			err := s.Settle(time.Second)
			if recovery <= time.Second {
				if err != nil || p.Down() {
					t.Fatalf("scheduled recovery did not settle: down=%v err=%v", p.Down(), err)
				}
				requireSettleRecords(t, sink, 1)
			} else {
				var u *qenv.Unsettled
				if !errors.As(err, &u) || len(u.DownProcesses) != 1 || !p.Down() {
					t.Fatalf("recovery beyond bound certified: %+v", err)
				}
				requireSettleRecords(t, sink, 0)
			}
		})
	}
}

func TestSettleCoreRequiresOnlyItsDownProcesses(t *testing.T) {
	s, sink := downSettleSim(t)
	a := s.Process("a", false, func(context.Context) {})
	b := s.Process("b", false, func(context.Context) {})
	b.Crash()
	if err := s.SettleCore(time.Second, a); err != nil {
		t.Fatalf("excluded down replica blocked surviving core: %v", err)
	}
	requireSettleRecords(t, sink, 1)
	var u *qenv.Unsettled
	if err := s.SettleCore(time.Second, a, b); !errors.As(err, &u) || len(u.DownProcesses) != 1 || u.DownProcesses[0].Name != "b" {
		t.Fatalf("included down replica certified: %v", err)
	}
	requireSettleRecords(t, sink, 1)
}

// Intentional shutdown is observable with Run, but is not all-process recovery.
// A surviving core makes that narrower availability scope explicit.
func TestSettleAfterExplicitProcessExit(t *testing.T) {
	s, sink := downSettleSim(t)
	retired := s.Process("retired", false, func(context.Context) {})
	survivor := s.Process("survivor", false, func(context.Context) {})
	retired.Exit()
	if err := s.Run(time.Second); err != nil {
		t.Fatalf("shutdown observation failed: %v", err)
	}
	if err := s.Settle(time.Second); !errors.Is(err, qenv.ErrUnsettled) {
		t.Fatalf("intentional down process certified whole-system recovery: %v", err)
	}
	requireSettleRecords(t, sink, 0)
	if err := s.SettleCore(time.Second, survivor); err != nil {
		t.Fatalf("explicit surviving scope did not settle: %v", err)
	}
	requireSettleRecords(t, sink, 1)
}

// An excluded replica's timerless wait cannot become a core obligation simply
// because there are no timers left to move the simulation's clock.
func TestSettleCoreStuckAccountingUsesRequiredScope(t *testing.T) {
	for _, mode := range []string{"excluded", "core-blocked", "client-blocked", "all", "run"} {
		t.Run(mode, func(t *testing.T) {
			s, sink := downSettleSim(t)
			core := s.Process("core", false, func(ctx context.Context) {
				if mode == "core-blocked" {
					qenv.Wait(ctx, func() bool { return false })
				}
			})
			s.Process("excluded", false, func(ctx context.Context) {
				qenv.Wait(ctx, func() bool { return false })
			})
			if mode == "client-blocked" {
				s.Spawn("client", func(ctx context.Context) { qenv.Wait(ctx, func() bool { return false }) })
			}
			var err error
			switch mode {
			case "all":
				err = s.Settle(time.Second)
			case "run":
				err = s.Run(time.Second)
			default:
				err = s.SettleCore(time.Second, core)
			}
			if mode == "excluded" {
				if err != nil {
					t.Fatalf("excluded wait blocked required scope: %v", err)
				}
				requireSettleRecords(t, sink, 1)
				return
			}
			if !errors.Is(err, qenv.ErrStuck) {
				t.Fatalf("required timerless wait lost stuck outcome: %v", err)
			}
			if mode != "run" && !errors.Is(err, qenv.ErrUnsettled) {
				t.Fatalf("required wait certified settlement: %v", err)
			}
			if mode == "core-blocked" || mode == "client-blocked" {
				if strings.Contains(err.Error(), "excluded.main") {
					t.Fatalf("outside task leaked into core completion reason: %v", err)
				}
			}
			requireSettleRecords(t, sink, 0)
		})
	}
}

func TestSettleDownIdentitySnapshotsDisambiguateNames(t *testing.T) {
	s, sink := downSettleSim(t)
	var ps []*qenv.Process
	for _, name := range []string{"z", "a", "a"} {
		p := s.Process(name, false, func(context.Context) {})
		p.Crash()
		ps = append(ps, p)
	}
	var u *qenv.Unsettled
	if err := s.Settle(time.Second); !errors.As(err, &u) {
		t.Fatal(err)
	}
	want := []qenv.ProcessIdentity{{Name: "a", Incarnation: 1, Index: 2}, {Name: "a", Incarnation: 1, Index: 3}, {Name: "z", Incarnation: 1, Index: 1}}
	if !slices.Equal(u.DownProcesses, want) {
		t.Fatalf("identities = %+v, want %+v", u.DownProcesses, want)
	}
	for _, p := range ps {
		p.Restart()
		p.Name = "renamed"
	}
	if !slices.Equal(u.DownProcesses, want) {
		t.Fatal("process mutation changed captured identities")
	}
	requireSettleRecords(t, sink, 0)
}

func TestSettleRetainsDownIdentitiesWithStuckCause(t *testing.T) {
	s, sink := downSettleSim(t)
	p := s.Process("offline", false, func(context.Context) {})
	p.Crash()
	s.Spawn("blocked-client", func(ctx context.Context) { qenv.Wait(ctx, func() bool { return false }) })
	err := s.Settle(time.Second)
	var u *qenv.Unsettled
	if !errors.As(err, &u) || !errors.Is(err, qenv.ErrStuck) || !errors.Is(err, qenv.ErrUnsettled) {
		t.Fatalf("lost stuck or unsettled classification: %v", err)
	}
	if len(u.DownProcesses) != 1 || len(u.Tasks) != 1 || !strings.Contains(err.Error(), "offline#1") || !strings.Contains(err.Error(), "blocked-client") {
		t.Fatalf("lost combined availability/task context: %+v", u)
	}
	requireSettleRecords(t, sink, 0)
}

func TestSettleNativeDoesNotCertifyDownProcesses(t *testing.T) {
	sink := &qobs.MemorySink{}
	rec := qobs.NewRecorder(sink)
	s := qenv.NewNative(qenv.WithContext(qenv.Nominal{}, qobs.WithRecorder(t.Context(), rec)), nil)
	defer s.Close()
	p := s.Process("offline", false, func(context.Context) {})
	p.Crash()
	if err := s.Settle(time.Millisecond); err != nil {
		t.Fatalf("native runtime acquired deterministic certification: %v", err)
	}
	requireSettleRecords(t, sink, 0)
}
