package qtest

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/internal/hooks"
	"github.com/chucklehead-dev/gotogether/validate"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
)

func TestFaultOrderConfiguration(t *testing.T) {
	good := []qenv.Scripted{{Kind: "a", Option: 1}, {Kind: "b", Name: "failed"}}
	for _, tc := range []struct {
		name string
		cfg  FaultOrderConfig
	}{
		{"empty", FaultOrderConfig{}},
		{"one", FaultOrderConfig{Faults: good[:1]}},
		{"duplicate", FaultOrderConfig{Faults: []qenv.Scripted{good[0], good[0]}}},
		{"empty-kind", FaultOrderConfig{Faults: []qenv.Scripted{{Option: 1}, good[1]}}},
		{"prefixed", FaultOrderConfig{Faults: []qenv.Scripted{{Kind: "fault.a", Option: 1}, good[1]}}},
		{"nominal", FaultOrderConfig{Faults: []qenv.Scripted{{Kind: "a"}, good[1]}}},
		{"negative-option", FaultOrderConfig{Faults: []qenv.Scripted{{Kind: "a", Option: -1}, good[1]}}},
		{"negative-after", FaultOrderConfig{Faults: []qenv.Scripted{{Kind: "a", Option: 1, After: -1}, good[1]}}},
		{"follows", FaultOrderConfig{Faults: []qenv.Scripted{{Kind: "a", Option: 1, Follows: []int{1}}, good[1]}}},
		{"negative-seeds", FaultOrderConfig{Faults: good, Seeds: -1}},
		{"negative-short", FaultOrderConfig{Faults: good, ShortSeeds: -1}},
		{"short-too-large", FaultOrderConfig{Faults: good, Seeds: 1, ShortSeeds: 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.cfg.validate(); err == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
	if err := (FaultOrderConfig{Faults: good}).validate(); err != nil {
		t.Fatal(err)
	}
	if err := (FaultOrderConfig{Faults: []qenv.Scripted{{Kind: "nemesis.crash.node"}, good[0]}}).validate(); err != nil {
		t.Fatalf("nemesis kind identifies the nonnominal fault: %v", err)
	}
}

func TestFaultOrderWitnessRequiresRequestedExecution(t *testing.T) {
	first, second := qenv.Scripted{Kind: "a", Option: 1, After: 2}, qenv.Scripted{Kind: "b", Name: "failed"}
	positive := []qenv.Choice{{Kind: "fault.a", Value: 1, Step: 2}, {Kind: "fault.b", Value: 2, Name: "failed", Step: 3}}
	for _, tc := range []struct {
		name string
		run  Run
		want bool
	}{
		{"positive", Run{scripted: true, Choices: positive}, true},
		{"remaining-is-not-provenance", Run{Choices: positive}, false},
		{"nominal", Run{scripted: true, Choices: []qenv.Choice{{Kind: "fault.a", Value: 0, Step: 2}, {Kind: "fault.b", Value: 0, Name: "failed"}}}, false},
		{"named-nominal", Run{scripted: true, Choices: []qenv.Choice{positive[0], {Kind: "fault.b", Value: 0, Name: "failed"}}}, false},
		{"wrong-option", Run{scripted: true, Choices: []qenv.Choice{{Kind: "fault.a", Value: 2, Step: 2}, positive[1]}}, false},
		{"wrong-name", Run{scripted: true, Choices: []qenv.Choice{positive[0], {Kind: "fault.b", Value: 1, Name: "other"}}}, false},
		{"before-after", Run{scripted: true, Choices: []qenv.Choice{{Kind: "fault.a", Value: 1, Step: 1}, positive[1]}}, false},
		{"reversed", Run{scripted: true, Choices: []qenv.Choice{positive[1], positive[0]}}, false},
		{"one-kind-only", Run{scripted: true, Choices: positive[:1]}, false},
		{"incomplete", Run{scripted: true, Choices: positive, Unscripted: []qenv.Scripted{second}}, false},
		{"misnamed", Run{scripted: true, Choices: positive, misnamed: []string{"bad fault"}}, false},
		{"execution-error", Run{scripted: true, Choices: positive, Err: errors.New("did not finish")}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.run.origin = origin{kind: "seed", seed: 7}
			tc.run.Report = &validate.Report{}
			x := &Exploration{test: "TestNative", id: 1, Runs: []*Run{&tc.run}}
			o := &FaultOrder{First: first, Second: second, Exploration: x, Budget: 3}
			o.refresh()
			if got := o.Status == "witnessed"; got != tc.want {
				t.Fatalf("status=%s want witness=%v", o.Status, tc.want)
			}
			if tc.want && o.Witness != "TestNative#1:seed=7" {
				t.Fatal(o.Witness)
			}
			if !tc.want && o.Witness != "" {
				t.Fatalf("unreached witness %q", o.Witness)
			}
		})
	}
	n := qenv.Scripted{Kind: "nemesis.crash.node", Option: 1}
	if !faultOrderChoice(qenv.Choice{Kind: "fault.nemesis.crash", Value: 2, Name: "crash.node"}, n) || faultOrderChoice(qenv.Choice{Kind: "fault.nemesis.crash", Value: 1, Name: "crash.other"}, n) || faultOrderChoice(qenv.Choice{Kind: "nemesis.crash", Value: 1, Name: "crash.node"}, n) {
		t.Fatal("nemesis identity confused with its enabled-list option")
	}
}

func TestFaultOrderAttemptsKeepOrdinaryScriptStrict(t *testing.T) {
	names := &hooks.FaultNames{}
	names.AddKind("a")
	names.AddKind("b")
	missing := &Run{scripted: true, faults: names, Unscripted: []qenv.Scripted{{Kind: "a", Option: 1}}}
	for _, partial := range []bool{false, true} {
		f := &failTB{TB: t}
		x := &Exploration{Runs: []*Run{missing}}
		if partial {
			x.faultOrder = &FaultOrder{First: qenv.Scripted{Kind: "a", Option: 1}, Second: qenv.Scripted{Kind: "b", Option: 1}}
		}
		x.unknownScripted(f)
		if got := len(f.errs) > 0; got == partial {
			t.Fatalf("partial=%v errors=%v", partial, f.errs)
		}
	}
	f := &failTB{TB: t}
	x := &Exploration{faultOrder: &FaultOrder{First: qenv.Scripted{Kind: "a", Option: 1}, Second: qenv.Scripted{Kind: "b", Option: 1}}, Runs: []*Run{{scripted: true, faults: names, misnamed: []string{"invalid option"}, Unscripted: missing.Unscripted}}}
	x.unknownScripted(f)
	if len(f.errs) != 1 || !strings.Contains(f.errs[0], "invalid option") {
		t.Fatalf("invalid configuration suppressed: %v", f.errs)
	}
}

func TestFaultOrdersUseNormalModelPipeline(t *testing.T) {
	RequireQuint(t)
	faults := []qenv.Scripted{{Kind: "test.ordered.a", Option: 1}, {Kind: "test.ordered.b", Name: "failed"}}
	before := slices.Clone(faults)
	a, b := qenv.DeclareFaults(faults[0].Kind, "failed"), qenv.DeclareFaults(faults[1].Kind, "failed")
	sys := System{Binding: "../validate/testdata/account.yaml", Run: func(c qenv.Chooser) error {
		s := qenv.New(c)
		defer s.Close()
		s.FaultOf(a)
		s.FaultOf(b)
		qobs.Record(s.Context(), "deposit", nil, "amount", 10, "obs.balance", 10)
		return nil
	}}
	calls := 0
	result := sys.FaultOrders(t, FaultOrderConfig{Faults: faults, Seeds: 2, Expect: func(first, second qenv.Scripted) []Claim {
		calls++
		return []Claim{Conforms()}
	}})
	if calls != 2 || len(result.Orders) != 2 {
		t.Fatalf("claims=%d orders=%d", calls, len(result.Orders))
	}
	for i, o := range result.Orders {
		if o.Exploration == nil || len(o.Exploration.Runs) != 2 || o.Budget != 2 {
			t.Fatalf("lost native attempts: %+v", o)
		}
		want := "witnessed"
		if i == 1 {
			want = "unreached"
		}
		if o.Status != want {
			t.Fatalf("order %d: %+v", i, o)
		}
		for _, r := range o.Exploration.Runs {
			if r.Report == nil || !r.Report.OK() {
				t.Fatalf("attempt unjudged: %+v", r)
			}
		}
	}
	for i := range faults {
		if faults[i].Kind != before[i].Kind || len(faults[i].Follows) != 0 {
			t.Fatal("configuration mutated")
		}
	}
}

func faultOrdersNativeSystem() System {
	a, b := qenv.DeclareFaults("test.ordered.a", "failed"), qenv.DeclareFaults("test.ordered.b", "failed")
	return System{Binding: "../validate/testdata/account.yaml", Run: func(c qenv.Chooser) error {
		s := qenv.New(c)
		defer s.Close()
		if s.FaultOf(a) == "" && os.Getenv("GO_WANT_FAULT_ORDER_PROCESS") == "named-nominal" {
			// A malformed adapter labels option 0 as the requested named fault.
			if log, ok := qenv.As[*qenv.ChoiceLog](c); ok {
				log.Name("failed")
			}
		}
		s.FaultOf(b)
		qobs.Record(s.Context(), "deposit", nil, "amount", 10, "obs.balance", 10)
		return nil
	}}
}

func TestFaultOrdersProcess(t *testing.T) {
	mode := os.Getenv("GO_WANT_FAULT_ORDER_PROCESS")
	if mode == "" {
		return
	}
	faults := []qenv.Scripted{{Kind: "test.ordered.a", Name: "failed"}, {Kind: "test.ordered.b", Name: "failed"}}
	sys := faultOrdersNativeSystem()
	ref := t.Name() + "/test.ordered.a_then_test.ordered.b#1:"
	switch mode {
	case "seed":
		t.Setenv("GOTOGETHER_RUN", ref+"seed=2")
	case "choices":
		t.Setenv("GOTOGETHER_RUN", ref+"choices=must-not-be-opened")
	case "corpus":
		t.Setenv("GOTOGETHER_CORPUS", t.TempDir())
	case "error":
		sys.Run = func(qenv.Chooser) error { return errors.New("ordered-attempt-execution-failed") }
	case "misnamed":
		faults[0].Name = "not-an-option"
	case "named-nominal":
	case "unnamed-kind-name":
		faults[1] = qenv.Scripted{Kind: "test.order.unnamed", Name: "failed"}
		sys.Run = func(c qenv.Chooser) error {
			s := qenv.New(c)
			defer s.Close()
			s.Choose("fault.test.ordered.a", 2)
			s.Choose("fault.test.order.unnamed", 2)
			qobs.Record(s.Context(), "deposit", nil, "amount", 10, "obs.balance", 10)
			return nil
		}
	case "dynamic-other", "option-collision", "prefix-target":
		missing := "test.ordered.unregistered"
		if mode == "option-collision" {
			missing = "test.ordered.a.failed"
		}
		if mode == "prefix-target" {
			missing = "test.ordered"
		}
		other := qenv.New(qenv.Nominal{})
		other.Spawn("other-system", func(context.Context) { qenv.DeclareFaults(missing, "failed") })
		if err := other.Run(time.Second); err != nil {
			t.Fatal(err)
		}
		other.Close()
		faults[1] = qenv.Scripted{Kind: missing, Option: 1}
	default:
		t.Fatalf("unknown child mode %q", mode)
	}
	result := sys.FaultOrders(t, FaultOrderConfig{Faults: faults, Seeds: 3})
	if mode == "seed" {
		o := result.Orders[0]
		if o.Budget != 3 || len(o.Exploration.Runs) != 1 || o.Status != "witnessed" || !strings.HasSuffix(o.Witness, ":seed=2") {
			t.Fatalf("seed reproduction lost request scope: %+v", o)
		}
	}
}

func TestFaultOrdersSeedReplayAndRefusals(t *testing.T) {
	RequireQuint(t)
	for _, tc := range []struct{ mode, want string }{
		{"seed", ""}, {"choices", "choices replay bypasses"}, {"corpus", ""},
		{"error", "ordered-attempt-execution-failed"}, {"misnamed", "has no option"}, {"named-nominal", "observed choice is nominal option 0"},
		{"unnamed-kind-name", "has no nonnominal option named"},
		{"dynamic-other", "not an exact fault kind"}, {"option-collision", "not an exact fault kind"}, {"prefix-target", "not an exact fault kind"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestFaultOrdersProcess$", "-test.v")
			for _, value := range os.Environ() {
				if !strings.HasPrefix(value, "GOTOGETHER_RUN=") && !strings.HasPrefix(value, "GOTOGETHER_CORPUS=") && !strings.HasPrefix(value, "GOTOGETHER_EVIDENCE=") && !strings.HasPrefix(value, "GOTOGETHER_KEEP=") && !strings.HasPrefix(value, "GO_WANT_FAULT_ORDER_PROCESS=") {
					cmd.Env = append(cmd.Env, value)
				}
			}
			cmd.Env = append(cmd.Env, "GO_WANT_FAULT_ORDER_PROCESS="+tc.mode)
			out, err := cmd.CombinedOutput()
			if tc.want == "" {
				if err != nil {
					t.Fatalf("seed replay: %v\n%s", err, out)
				}
			} else if err == nil || !strings.Contains(string(out), tc.want) {
				t.Fatalf("%s: err=%v want %q\n%s", tc.mode, err, tc.want, out)
			}
		})
	}
}

func TestFaultOrdersDefaultNemesisEvidence(t *testing.T) {
	RequireQuint(t)
	kind := qenv.DeclareFaults("test.order.nemesis.request", "failed")
	sys := System{Binding: "../validate/testdata/account.yaml", Run: func(c qenv.Chooser) error {
		s := qenv.New(c)
		defer s.Close()
		bumped := false
		s.Nemesis("order.bump", func() bool { return !bumped }, func() { bumped = true })
		s.Spawn("client", func(ctx context.Context) {
			for range 3 {
				s.FaultOf(kind)
				qenv.Sleep(ctx, time.Millisecond)
			}
			qobs.Record(ctx, "deposit", nil, "amount", 10, "obs.balance", 10)
		})
		return s.Run(time.Second)
	}}
	r := sys.FaultOrders(t, FaultOrderConfig{Seeds: 2, Faults: []qenv.Scripted{{Kind: "nemesis.order.bump"}, {Kind: kind.Name, Name: "failed"}}})
	for _, o := range r.Orders {
		if o.Status != "witnessed" {
			t.Fatalf("nemesis pair not witnessed: %+v", o)
		}
	}
	path := filepath.Join(t.TempDir(), "orders.json")
	if err := r.Evidence().WriteJSON(path); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadEvidence(path); err != nil {
		t.Fatalf("native nemesis evidence rejected: %v", err)
	}
}

func TestFaultOrdersDeclaredButUnreachedTarget(t *testing.T) {
	RequireQuint(t)
	sys := System{Binding: "../validate/testdata/account.yaml", Run: func(c qenv.Chooser) error {
		s := qenv.New(c)
		defer s.Close()
		s.Spawn("client", func(ctx context.Context) {
			a := qenv.DeclareFaults("test.order.declared.a", "failed")
			qenv.DeclareFaults("test.order.declared.never", "failed")
			s.FaultOf(a)
			qobs.Record(ctx, "deposit", nil, "amount", 10, "obs.balance", 10)
		})
		return s.Run(time.Second)
	}}
	r := sys.FaultOrders(t, FaultOrderConfig{Seeds: 1, Faults: []qenv.Scripted{{Kind: "test.order.declared.a", Option: 1}, {Kind: "test.order.declared.never", Name: "failed"}}})
	for _, o := range r.Orders {
		if o.Status != "unreached" {
			t.Fatalf("unreached declaration became witness: %+v", o)
		}
	}
}

func TestFaultOrdersChoiceOnlyTargets(t *testing.T) {
	RequireQuint(t)
	sys := System{Binding: "../validate/testdata/account.yaml", Run: func(c qenv.Chooser) error {
		s := qenv.New(c)
		defer s.Close()
		s.Choose("fault.test.order.choice.a", 2)
		s.Choose("fault.test.order.choice.b", 2)
		qobs.Record(s.Context(), "deposit", nil, "amount", 10, "obs.balance", 10)
		return nil
	}}
	r := sys.FaultOrders(t, FaultOrderConfig{Seeds: 1, Faults: []qenv.Scripted{{Kind: "test.order.choice.a", Option: 1}, {Kind: "test.order.choice.b", Option: 1}}})
	if r.Orders[0].Status != "witnessed" || r.Orders[1].Status != "unreached" {
		t.Fatalf("actual choice kinds were lost: %+v", r.Orders)
	}
}
