package qenv_test

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// upgraded runs two nodes that log the version they boot as, every
// second, under c, with an upgrade to v2 registered for both.
func upgraded(t *testing.T, c qenv.Chooser) (log []string, a, b *qenv.Process) {
	t.Helper()
	s := qenv.New(c)
	defer s.Close()
	node := func(version string) func(ctx context.Context) {
		return func(ctx context.Context) {
			p := qenv.ProcessOf(ctx)
			for range 5 {
				log = append(log, fmt.Sprintf("%s %s#%d", version, p.Name, p.Incarnation))
				qenv.Sleep(ctx, time.Second)
			}
		}
	}
	a = s.Process("a", false, node("v1"))
	b = s.Process("b", false, node("v1"))
	s.Upgrades("v2", node("v2"), a, b)
	if err := s.Run(time.Minute); err != nil {
		t.Fatal(err)
	}
	return log, a, b
}

// A scripted upgrade restarts its process as the new version where the
// script says, and no other: b runs v2 from its third step, a stays v1.
// Processes need not be crashable to be upgraded.
func TestUpgradeScripted(t *testing.T) {
	sc := &qenv.Script{Faults: []qenv.Scripted{{Kind: "nemesis.upgrade.b", After: 3}}}
	log, a, b := upgraded(t, sc)
	if a.Version != "" || a.Incarnation != 1 || b.Version != "v2" || b.Incarnation != 2 {
		t.Errorf("a %q#%d, b %q#%d: want a as created, b upgraded once", a.Version, a.Incarnation, b.Version, b.Incarnation)
	}
	seen := map[string]bool{}
	for _, l := range log {
		seen[l] = true
	}
	if !seen["v2 b#2"] || seen["v2 a#2"] || !seen["v1 a#1"] {
		t.Errorf("log %v: want b's second incarnation on v2, a on v1 throughout", log)
	}
	if r := sc.Remaining(); len(r) != 0 {
		t.Errorf("the upgrade never happened: %v", r)
	}
}

// At a rate of one, the rollout happens at once and only once a process:
// an upgraded process's nemesis is off. A run's choices replay it.
func TestUpgradesRollOutOnceAndReplay(t *testing.T) {
	seeded := qenv.NewSeeded(3)
	seeded.Rates = qenv.NoFaults.Set("nemesis.upgrade", 1)
	rec := &qenv.ChoiceLog{C: seeded}
	log, a, b := upgraded(t, rec)
	if a.Version != "v2" || b.Version != "v2" || a.Incarnation != 2 || b.Incarnation != 2 {
		t.Errorf("a %q#%d, b %q#%d: want both upgraded once", a.Version, a.Incarnation, b.Version, b.Incarnation)
	}
	again, _, _ := upgraded(t, qenv.Replay(rec.Choices))
	if !reflect.DeepEqual(log, again) {
		t.Errorf("replayed %v, ran %v", again, log)
	}
}

// An upgrade's new code is in place before its crash: a supervisor that
// restarts the process from OnCrash boots v2 (it once rebooted v1, the
// upgrade doing nothing, an exploration never mixing versions).
func TestUpgradeUnderASupervisor(t *testing.T) {
	var booted []string
	s := qenv.New(&qenv.Script{Faults: []qenv.Scripted{{Kind: "nemesis.upgrade.a", After: 1}}, Progress: func() int { return len(booted) }})
	defer s.Close()
	node := func(v string) func(ctx context.Context) {
		return func(ctx context.Context) {
			booted = append(booted, v)
			qenv.Sleep(ctx, 5*time.Second)
		}
	}
	a := s.Process("a", false, node("v1"))
	s.OnCrash(func(p *qenv.Process) { p.Restart() })
	s.Upgrades("v2", node("v2"), a)
	if err := s.Run(time.Minute); err != nil {
		t.Fatal(err)
	}
	if a.Version != "v2" || !reflect.DeepEqual(booted, []string{"v1", "v2"}) {
		t.Errorf("version %q, booted %v: want v1 then v2", a.Version, booted)
	}
}

// A process cannot upgrade itself, and a second rollout of one process is
// refused: each panics with the fix, where they once failed silently (a
// process down for good; two nemeses undoing each other).
func TestUpgradeRefusals(t *testing.T) {
	panics := func(f func()) (msg string) {
		defer func() { msg = fmt.Sprint(recover()) }()
		f()
		return ""
	}
	s := qenv.New(qenv.NewSeeded(1))
	a := s.Process("a", false, func(ctx context.Context) {})
	s.Upgrades("v2", func(ctx context.Context) {}, a)
	if msg := panics(func() { s.Upgrades("v3", func(ctx context.Context) {}, a) }); !strings.Contains(msg, "a second rollout of a") {
		t.Errorf("a second rollout: %q", msg)
	}
	s.Close()

	s = qenv.New(qenv.NewSeeded(1))
	defer s.Close()
	var msg string
	b := s.Process("b", false, func(ctx context.Context) {
		msg = panics(func() { qenv.ProcessOf(ctx).Upgrade("v2", func(ctx context.Context) {}) })
	})
	if err := s.Run(time.Minute); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(msg, "b upgrades itself") || b.Down() {
		t.Errorf("a self-upgrade: %q, down %v", msg, b.Down())
	}
}
