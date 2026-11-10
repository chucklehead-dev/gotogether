package qenv_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
)

type contractOptionsChooser struct {
	sim    *qenv.Sim
	choose func(string, int) int
}

func (c *contractOptionsChooser) ObserveSim(s *qenv.Sim)        { c.sim = s }
func (c *contractOptionsChooser) Choose(kind string, n int) int { return c.choose(kind, n) }

func TestChoiceOptionsDeclaredActivePrefix(t *testing.T) {
	kind := qenv.DeclareFaults("test.options-contract", "unavailable", "lostResponse")
	other := qenv.DeclareFaults("test.options-contract-other", "other")
	c := &contractOptionsChooser{}
	s := qenv.New(c)
	defer s.Close()
	if got := s.ChoiceOptions("fault." + kind.Name); got != nil {
		t.Fatalf("outside choice: %v", got)
	}
	var want []string
	var snapshots [][]string
	selected := 0
	c.choose = func(active string, n int) int {
		got := s.ChoiceOptions(active)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s(%d): options=%v, want %v", active, n, got, want)
		}
		if got := s.ChoiceOptions("fault." + other.Name); got != nil {
			t.Fatalf("wrong kind: %v", got)
		}
		if got := s.ChoiceOptions("not.a.fault"); got != nil {
			t.Fatalf("nonactive kind: %v", got)
		}
		if got != nil {
			snapshots = append(snapshots, append([]string(nil), got...))
			got[0] = "changed"
			if again := s.ChoiceOptions(active); !reflect.DeepEqual(again, want) {
				t.Fatalf("returned slice aliases registry: %v", again)
			}
		}
		return selected
	}
	want = []string{"nominal", "unavailable", "lostResponse"}
	if got := s.FaultOf(kind); got != "" {
		t.Fatalf("nominal option = %q", got)
	}
	want = []string{"nominal", "unavailable"}
	if got := s.FaultOf(kind.Only("unavailable")); got != "" {
		t.Fatal(got)
	}
	want = []string{"nominal", "unavailable", "lostResponse"}
	selected = 2
	if got := s.FaultOf(kind); got != "lostResponse" {
		t.Fatalf("full choice result = %q", got)
	}
	want = []string{"nominal", "unavailable"}
	selected = 1
	if got := s.FaultOf(kind.Only("unavailable")); got != "unavailable" {
		t.Fatalf("restricted choice result = %q", got)
	}
	selected = 0
	want = nil
	s.Choose("fault."+kind.Name, 4) // More choices than declared names cannot be described honestly.
	s.Choose("fault.test.options-contract-unknown", 2)
	s.Choose("custom.pick", 2)
	if got := s.ChoiceOptions("fault." + kind.Name); got != nil {
		t.Fatalf("stale choice: %v", got)
	}
	if !reflect.DeepEqual(snapshots, [][]string{{"nominal", "unavailable", "lostResponse"}, {"nominal", "unavailable"}, {"nominal", "unavailable", "lostResponse"}, {"nominal", "unavailable"}}) {
		t.Fatalf("snapshots=%v", snapshots)
	}
	if !reflect.DeepEqual(kind.Options, []string{"unavailable", "lostResponse"}) {
		t.Fatal("registry declaration mutated")
	}
}

func TestChoiceOptionsRestoresNestedAndPanickingChoices(t *testing.T) {
	outer := qenv.DeclareFaults("test.options-outer", "one", "two")
	inner := qenv.DeclareFaults("test.options-inner", "inner")
	c := &contractOptionsChooser{}
	s := qenv.New(c)
	defer s.Close()
	panicInner := false
	c.choose = func(kind string, n int) int {
		if kind == "fault."+inner.Name {
			if got := s.ChoiceOptions(kind); !reflect.DeepEqual(got, []string{"nominal", "inner"}) {
				t.Fatalf("nested options: %v", got)
			}
			if got := s.ChoiceOptions("fault." + outer.Name); got != nil {
				t.Fatalf("outer kind visible in inner: %v", got)
			}
			if panicInner {
				panic("nested chooser control")
			}
			return 0
		}
		want := []string{"nominal", "one", "two"}
		if got := s.ChoiceOptions(kind); !reflect.DeepEqual(got, want) {
			t.Fatalf("outer options: %v", got)
		}
		func() {
			defer func() {
				if r := recover(); r != nil && r != "nested chooser control" {
					panic(r)
				}
			}()
			s.FaultOf(inner)
		}()
		if got := s.ChoiceOptions(kind); !reflect.DeepEqual(got, want) {
			t.Fatalf("outer not restored: %v", got)
		}
		return 0
	}
	s.FaultOf(outer)
	panicInner = true
	s.FaultOf(outer)
	c.choose = func(string, int) int { panic("outer chooser control") }
	func() {
		defer func() {
			if r := recover(); r != "outer chooser control" {
				t.Fatalf("panic=%v", r)
			}
		}()
		s.FaultOf(outer)
	}()
	if got := s.ChoiceOptions("fault." + outer.Name); got != nil {
		t.Fatalf("panicked choice remains active: %v", got)
	}
}

func TestChoiceOptionsNestedFaultDuringSchedulerAndNemesis(t *testing.T) {
	fault := qenv.DeclareFaults("test.options-nested-scheduler", "fault")
	c := &contractOptionsChooser{}
	s := qenv.New(c)
	defer s.Close()
	sched, nem := false, false
	probing := false
	c.choose = func(kind string, n int) int {
		if probing {
			if got := s.ChoiceOptions(kind); got != nil {
				t.Fatalf("nested direct choice borrowed outer labels: %v", got)
			}
			return 0
		}
		got := s.ChoiceOptions(kind)
		if len(got) != n {
			t.Fatalf("%s: %v for %d", kind, got, n)
		}
		if kind == "fault."+fault.Name {
			if !reflect.DeepEqual(got, []string{"nominal", "fault"}) {
				t.Fatalf("outer context leaked: %v", got)
			}
			return 0
		}
		if kind == "sched.task" {
			sched = true
		} else {
			nem = true
		}
		saved := append([]string(nil), got...)
		probing = true
		s.Choose(kind, n+1)
		s.Choose(kind, n)
		probing = false
		s.FaultOf(fault)
		if got := s.ChoiceOptions(kind); !reflect.DeepEqual(got, saved) {
			t.Fatalf("outer names changed: %v != %v", got, saved)
		}
		return 0
	}
	for _, name := range []string{"a", "b"} {
		s.Process(name, true, func(ctx context.Context) { qenv.Yield(ctx); qenv.Yield(ctx) })
	}
	if err := s.Run(time.Second); err != nil {
		t.Fatal(err)
	}
	if !sched || !nem {
		t.Fatalf("missing real outer choices: sched=%v nem=%v", sched, nem)
	}
}

func TestChoiceOptionsCoreSettleNamesExternalNemesis(t *testing.T) {
	c := &contractOptionsChooser{}
	s := qenv.New(c)
	defer s.Close()
	s.NemesisEvery = time.Millisecond
	seen := false
	c.choose = func(kind string, n int) int {
		if kind == "fault.nemesis.crash" {
			seen = true
			if got := s.ChoiceOptions(kind); !reflect.DeepEqual(got, []string{"none", "crash.outside"}) || len(got) != n {
				t.Fatalf("core settle nemesis names: %v for %d", got, n)
			}
		}
		return 0
	}
	core := s.Process("core", false, func(ctx context.Context) { qenv.Sleep(ctx, 5*time.Millisecond) })
	s.Process("outside", true, func(ctx context.Context) {
		qenv.Daemon(ctx)
		for {
			qenv.Sleep(ctx, time.Millisecond)
		}
	})
	if err := s.SettleCore(time.Second, core); err != nil {
		t.Fatal(err)
	}
	if !seen {
		t.Fatal("no external nemesis observed during core settle")
	}
	if got := s.ChoiceOptions("fault.nemesis.crash"); got != nil {
		t.Fatalf("core settle choice stale: %v", got)
	}
}
