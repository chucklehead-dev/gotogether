package qtest

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// FaultOrderConfig samples every directed pair of distinct fault kinds. Each
// pair has a fresh script per seed, imposing the second fault after the first;
// all other faults are nominal. Faults may specify Option or Name and After,
// but not Follows: this workflow supplies the pair's precedence. As with
// qenv.Script, nemeses are selected by kind; their Option and Name are ignored.
//
// Seeds defaults to 3. ShortSeeds, when positive, is the budget under -short.
// Unlike an ordinary scripted exploration, an attempt may finish without
// reaching its requested pair. Such attempts are still checked against the
// model; an unwitnessed pair is bounded unreached, never proved impossible.
// Misnamed faults and execution errors still fail the test.
type FaultOrderConfig struct {
	Faults     []qenv.Scripted
	Seeds      int
	ShortSeeds int
	// Expect gives the model claims for every attempt of a requested pair.
	// Nil checks Conforms. Reaching the pair is a separate observation.
	Expect func(first, second qenv.Scripted) []Claim
}

// FaultOrder is one requested order and the native exploration that tried it.
// Status is "witnessed" or "unreached" within Budget attempted seeds. Witness
// names the first completing native run, or is empty. A replay of one seed
// retains the requested budget; Exploration says which runs actually ran.
type FaultOrder struct {
	First, Second qenv.Scripted
	Exploration   *Exploration
	Budget        int
	Status        string
	Witness       string
}

// FaultOrderResult retains the complete requested roster, including orders
// that the bounded attempts did not reach.
type FaultOrderResult struct {
	test   string // stable parent test identity
	Orders []*FaultOrder
}

// FaultOrders samples all directed pairs through the same capture, model,
// composition, replay and evidence pipeline as Explore. It does not require a
// full permutation of all kinds to be reachable in one phased application.
// Inspect the returned per-order status separately from the model claims.
// Corpus entries are additional model regressions, never directed-order witnesses.
// Seed reproduction retains the script; corpus-only replay retains regression
// evidence without directed attempts. Choices-file replay is refused.
func (s System) FaultOrders(t *testing.T, cfg FaultOrderConfig) *FaultOrderResult {
	t.Helper()
	if err := cfg.validate(); err != nil {
		t.Fatal(err)
	}
	cfg.Faults = slices.Clone(cfg.Faults)
	if cfg.Seeds == 0 {
		cfg.Seeds = 3
	}
	budget := cfg.Seeds
	if testing.Short() && cfg.ShortSeeds > 0 {
		budget = cfg.ShortSeeds
	}
	result := &FaultOrderResult{test: t.Name()}
	for i, first := range cfg.Faults {
		for j, second := range cfg.Faults {
			if i == j {
				continue
			}
			order := &FaultOrder{First: first, Second: second, Budget: budget, Status: "unreached"}
			result.Orders = append(result.Orders, order)
			t.Run(first.Kind+" then "+second.Kind, func(t *testing.T) {
				xc := exploreSpec{ExploreConfig: ExploreConfig{Seeds: cfg.Seeds, ShortSeeds: cfg.ShortSeeds}, opts: s.opts(), rates: s.FaultRates(), faultOrder: order}
				xc.Chooser = func(seed uint64) qenv.Chooser {
					last := second
					last.Follows = []int{0}
					return &qenv.Script{Base: qenv.NewSeeded(seed), Faults: []qenv.Scripted{first, last}}
				}
				order.Exploration = explore(t, s.Binding, xc, s.Run)
				order.refresh()
				want := []Claim{Conforms()}
				if cfg.Expect != nil {
					want = cfg.Expect(first, second)
				}
				order.Exploration.Expect(t, want...)
				t.Logf("qtest: %s then %s: %s in %d seed attempts (budget %d)", first.Kind, second.Kind, order.Status, len(order.faultEvidence().Attempts), order.Budget)
			})
		}
	}
	return result
}

func (c FaultOrderConfig) validate() error {
	if len(c.Faults) < 2 {
		return fmt.Errorf("qtest.FaultOrders: request at least two distinct fault kinds")
	}
	seeds := c.Seeds
	if seeds == 0 {
		seeds = 3
	}
	if seeds < 0 || c.ShortSeeds < 0 || c.ShortSeeds > seeds {
		return fmt.Errorf("qtest.FaultOrders: seed budgets must be nonnegative and ShortSeeds must not exceed Seeds")
	}
	seen := map[string]bool{}
	for _, f := range c.Faults {
		if f.Kind == "" || strings.HasPrefix(f.Kind, "fault.") {
			return fmt.Errorf("qtest.FaultOrders: name a nonempty fault kind without the fault. prefix, got %q", f.Kind)
		}
		if seen[f.Kind] {
			return fmt.Errorf("qtest.FaultOrders: duplicate kind %q: supply one fault per kind", f.Kind)
		}
		seen[f.Kind] = true
		if len(f.Follows) != 0 {
			return fmt.Errorf("qtest.FaultOrders: %s supplies Follows: the workflow supplies pair precedence", f.Kind)
		}
		if f.After < 0 || !strings.HasPrefix(f.Kind, "nemesis.") && f.Name == "" && f.Option <= 0 {
			return fmt.Errorf("qtest.FaultOrders: %s needs a nonnominal Option or Name and nonnegative After", f.Kind)
		}
	}
	return nil
}

// refresh derives witnesses from actual scripted execution and positive exact
// choices. Empty Remaining alone is insufficient: a replay or a nominal choice
// did not demonstrate the requested faults.
func (o *FaultOrder) refresh() {
	o.Status, o.Witness = "unreached", ""
	if o.Exploration == nil {
		return
	}
	for _, r := range o.Exploration.Runs {
		if r == nil || r.Err != nil || r.Report == nil || !r.scripted || len(r.Unscripted) != 0 || len(r.misnamed) != 0 {
			continue
		}
		first := false
		for _, c := range r.Choices {
			if !first {
				first = faultOrderChoice(c, o.First)
			} else if faultOrderChoice(c, o.Second) {
				o.Status, o.Witness = "witnessed", o.Exploration.ref()+":"+r.ID()
				return
			}
		}
	}
}

func faultOrderChoice(c qenv.Choice, f qenv.Scripted) bool {
	if !qenv.IsFault(c.Kind) || c.Value <= 0 || c.Step < f.After {
		return false
	}
	kind := strings.TrimPrefix(c.Kind, "fault.")
	if strings.HasPrefix(f.Kind, "nemesis.") {
		return strings.HasPrefix(kind, "nemesis.") && "nemesis."+c.Name == f.Kind
	}
	if kind != f.Kind {
		return false
	}
	if f.Name != "" {
		return c.Name == f.Name
	}
	return c.Value == f.Option
}
