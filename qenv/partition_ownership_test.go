package qenv_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
)

func TestPartitionHooksRetainFirstCutArguments(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	defer s.Close()
	a := s.Process("a", false, func(context.Context) {})
	b := s.Process("b", false, func(context.Context) {})
	var events []string
	s.Net().OnCut(func(x, y *qenv.Process) { events = append(events, "cut "+x.Name+y.Name) })
	s.Net().OnHeal(func(x, y *qenv.Process) { events = append(events, "heal "+x.Name+y.Name) })
	s.Net().Partition(b, a)
	s.Net().Partition(a, b) // The existing edge retains its first cut's arguments.
	if !s.Net().Partitioned(a, b) || !s.Net().Partitioned(b, a) {
		t.Fatal("cut must be symmetric")
	}
	s.Net().Heal(a, b)
	s.Net().Heal(b, a) // An already healed edge has no second callback.
	s.Net().Partition(a, b)
	s.Net().Heal(b, a)
	if want := []string{"cut ba", "heal ba", "cut ab", "heal ab"}; !slices.Equal(events, want) {
		t.Fatalf("cut/heal callback arguments: %v, want %v", events, want)
	}
	if s.Net().Partitioned(a, b) {
		t.Fatal("healed edge remains partitioned")
	}
}

func TestHealCallbackRepartitionsSameEdge(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	defer s.Close()
	a := s.Process("a", false, func(context.Context) {})
	b := s.Process("b", false, func(context.Context) {})
	var healed [][2]*qenv.Process
	s.Net().OnHeal(func(x, y *qenv.Process) {
		healed = append(healed, [2]*qenv.Process{x, y})
		if len(healed) == 1 {
			s.Net().Partition(a, b)
		}
	})
	s.Net().Partition(b, a)
	s.Net().Heal(a, b)
	if !s.Net().Partitioned(a, b) {
		t.Fatal("Heal removed the callback's new cut of the same pair")
	}
	s.Net().Heal(b, a)
	if want := [][2]*qenv.Process{{b, a}, {a, b}}; !slices.Equal(healed, want) {
		t.Fatalf("repartitioned edge callbacks: %v, want %v", healed, want)
	}
	if s.Net().Partitioned(a, b) {
		t.Fatal("second cut remains after its own Heal")
	}
}

// A callback may make a new cut while Settle reports an older batch of heals.
// The old batch must retain its own arguments and leave the new cut's metadata
// available for that new cut's eventual Heal.
func TestSettleHealCallbackRecutRetainsBothEdges(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	defer s.Close()
	a := s.Process("a", false, func(context.Context) {})
	b := s.Process("b", false, func(context.Context) {})
	c := s.Process("c", false, func(context.Context) {})
	d := s.Process("d", false, func(context.Context) {})
	var healed [][2]*qenv.Process
	s.Net().OnHeal(func(x, y *qenv.Process) {
		healed = append(healed, [2]*qenv.Process{x, y})
		if x == a && y == b {
			s.Net().Partition(d, c)
		}
	})
	s.Net().Partition(a, b)
	s.Net().Partition(c, d)
	if err := s.Settle(time.Second); err != nil {
		t.Fatal(err)
	}
	if want := [][2]*qenv.Process{{a, b}, {c, d}}; !slices.Equal(healed, want) {
		t.Errorf("Settle heal callbacks: %v, want original edges %v", healed, want)
	}
	if !s.Net().Partitioned(c, d) || s.Net().Partitioned(a, b) {
		t.Fatal("callback's new cut did not remain independently active")
	}
	s.Net().Heal(c, d)
	s.Net().Heal(d, c)
	if want := [][2]*qenv.Process{{a, b}, {c, d}, {d, c}}; !slices.Equal(healed, want) {
		t.Fatalf("new edge's Heal callbacks: %v, want %v", healed, want)
	}
}

func TestExpiredHealerCannotEndRepartitionedEdge(t *testing.T) {
	s := qenv.New(&qenv.Script{Faults: []qenv.Scripted{{Kind: "nemesis.partition.a.b"}}})
	defer s.Close()
	a := s.Process("a", false, func(context.Context) {})
	b := s.Process("b", false, func(context.Context) {})
	s.Net().CutLength = 10 * time.Millisecond
	s.Net().PartitionNemesis(a, b)
	heals := 0
	s.Net().OnHeal(func(x, y *qenv.Process) { heals++ })
	stillCut := false
	s.Spawn("repartition", func(ctx context.Context) {
		if !qenv.WaitFor(ctx, func() bool { return s.Net().Partitioned(a, b) }, time.Second) {
			t.Error("scripted nemesis never cut the edge")
			return
		}
		s.Net().Heal(b, a)
		s.Net().Partition(b, a)
		qenv.Sleep(ctx, 50*time.Millisecond)
		stillCut = s.Net().Partitioned(a, b)
	})
	if err := s.Run(time.Second); err != nil {
		t.Fatal(err)
	}
	if !stillCut || heals != 1 {
		t.Fatalf("old timed healer affected new cut: partitioned=%v, heals=%d", stillCut, heals)
	}
	s.Net().Heal(a, b)
	if heals != 2 || s.Net().Partitioned(a, b) {
		t.Fatalf("new cut did not heal independently: heals=%d", heals)
	}
}
