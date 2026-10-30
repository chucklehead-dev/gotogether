package runprofile

import (
	"reflect"
	"strings"
	"testing"
)

func TestProfileTextRetainsPoliciesAndDistinguishesReplay(t *testing.T) {
	p := Profile{
		ID: "TestX#1", Mode: "seeded", Chooser: "seeded", Parallel: 4,
		Replay: "seed=999", Seeds: &Seeds{Count: 50},
		Rates:      &Rates{Default: 0.05, Kinds: map[string]float64{"store.z": 0.3, "rpc.a": 0}},
		Validation: Validation{Binding: "model.yaml", Constants: map[string]string{"z": "2", "a": "1"}, Invariants: []string{"zInv", "aInv"}, Quint: "/tools/quint", Backend: "rust", Eval: "shadow", ConfirmationRate: 0.2, ConfirmationSeed: "ci"},
		Check:      true, AllFaults: true, Corpus: "saved-runs",
	}
	text := p.String()
	for _, want := range []string{"effective run profile TestX#1", "replay seed=999", "default 0.05", "rpc.a: 0", "store.z: 0.3", "constant a = 1", "constant z = 2", "invariants: aInv, zInv", "/tools/quint", "shadow", "0.2", "ci", "self-checks true", "all fault families true", "saved-runs"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "50 seeds") || strings.Index(text, "rpc.a:") > strings.Index(text, "store.z:") {
		t.Fatalf("replay claims unused sample, or fault overrides unstable:\n%s", text)
	}
}

func TestProfileTextRetainsSearchExclusionsWithoutMutatingPlan(t *testing.T) {
	excluded := []string{"store.z", "rpc.a"}
	p := Profile{Mode: "exhaustive", Exhaust: &Exhaust{Delays: 2, Faults: 1, MaxRuns: 20, Partial: true, Without: excluded}, Validation: Validation{Search: &Search{Linearize: true, OrderBudget: 7, ResolvePending: true, ResolveBudget: 11, Key: "account", ClockSkew: "2ms"}}}
	text := p.String()
	for _, want := range []string{"max runs 20", "partial true", "rpc.a, store.z", "order budget 7", "resolve budget 11", "partition key account", "clock skew 2ms"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q:\n%s", want, text)
		}
	}
	if !reflect.DeepEqual(excluded, []string{"store.z", "rpc.a"}) {
		t.Fatal("rendering changed execution policy")
	}
}

func TestNavigationProfileShowsForcedPolicy(t *testing.T) {
	p := Profile{Mode: "navigation", Chooser: "navigation", Parallel: 1, Replay: "TestX#1:seed=7@0:1@2"}
	text := p.String()
	if !strings.Contains(text, "fixed choice prefix; nominal continuation") || strings.Contains(text, "fault probability") || strings.Contains(text, "initial sample") || strings.Contains(text, "custom chooser") {
		t.Fatalf("navigation misrepresented seeded policy:\n%s", text)
	}
}
