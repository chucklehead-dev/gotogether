// Package runprofile describes the effective configuration of a driven run.
// It contains data only, independent of simulation and validation machinery.
package runprofile

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// Profile is a resolved exploration configuration. It describes configuration,
// not coverage: observed limits and judgments remain in their check receipts.
type Profile struct {
	ID       string `json:"id"`
	Mutant   string `json:"mutant,omitempty"`
	Mode     string `json:"mode"`
	Parallel int    `json:"parallel"`
	Chooser  string `json:"chooser"`
	// Without lists fault kinds forced nominal by this workflow.
	Without    []string   `json:"without,omitempty"`
	Seeds      *Seeds     `json:"seeds,omitempty"`
	Rates      *Rates     `json:"rates,omitempty"`
	Validation Validation `json:"validation"`
	Exhaust    *Exhaust   `json:"exhaust,omitempty"`
	Replay     string     `json:"replay,omitempty"`
	Corpus     string     `json:"corpus,omitempty"`
	AllFaults  bool       `json:"allFaults"`
	Check      bool       `json:"check"`
}

// Seeds describes the seeded sample initially planned and its optional top-up.
// Offset is the value before the first seed. Replay supersedes this sample.
type Seeds struct {
	Count   int    `json:"count"`
	Offset  uint64 `json:"offset"`
	Short   bool   `json:"short"`
	TopUpTo int    `json:"topUpTo,omitempty"`
	Steer   int    `json:"steer"`
}

// Rates describes the probability of nonnominal fault choices. It is absent
// for opaque custom choosers and exhaustive schedules, which do not use it.
type Rates struct {
	Default float64            `json:"default"`
	Kinds   map[string]float64 `json:"kinds"`
}

// Validation describes the primary checker validation policy. Additional
// composition checkers retain their own explicitly supplied options. Backend "automatic" preserves batch
// backend selection (TypeScript batches with Rust failure details).
type Validation struct {
	// ChoiceLimit bounds expanded finite model-choice states per actor.
	ChoiceLimit int `json:"choiceLimit"`
	// Binding identifies the primary checker; empty means no primary model.
	Binding string `json:"binding"`
	// Constants and Invariants are resolved from the binding and workflow options.
	Constants  map[string]string `json:"constants,omitempty"`
	Invariants []string          `json:"invariants"`
	// ConfirmationRate is the configured deterministic sample rate of evaluator
	// passes confirmed by Quint; unconfirmed actions have a one-in-five floor.
	ConfirmationRate float64 `json:"confirmationRate"`
	ConfirmationSeed string  `json:"confirmationSeed,omitempty"`
	Quint            string  `json:"quint"`
	Backend          string  `json:"backend"`
	Eval             string  `json:"eval"`
	Search           *Search `json:"search,omitempty"`
}

// Search describes the primary history ordering and pending-effect searches.
type Search struct {
	Linearize      bool   `json:"linearize"`
	Key            string `json:"key,omitempty"`
	OrderBudget    int    `json:"orderBudget,omitempty"`
	ResolvePending bool   `json:"resolvePending"`
	ResolveBudget  int    `json:"resolveBudget,omitempty"`
	ClockSkew      string `json:"clockSkew"`
}

// Exhaust describes the limits of a bounded schedule search.
type Exhaust struct {
	Delays  int      `json:"delays"`
	Faults  int      `json:"faults"`
	Without []string `json:"without"`
	MaxRuns int      `json:"maxRuns"`
	Partial bool     `json:"partial"`
}

// String renders effective configuration without implying observed coverage.
func (p Profile) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "effective run profile %s:\n  mode %s; chooser %s; parallel %d", p.ID, p.Mode, p.Chooser, p.Parallel)
	if p.Mutant != "" {
		fmt.Fprintf(&b, "\n  mutant %s", p.Mutant)
	}
	if p.Replay != "" {
		fmt.Fprintf(&b, "\n  replay %s", p.Replay)
	} else if p.Seeds != nil {
		s := p.Seeds
		fmt.Fprintf(&b, "\n  initial sample %d seeds from %d", s.Count, s.Offset+1)
		if s.Short {
			b.WriteString(" (short)")
		}
		if s.TopUpTo > s.Count {
			fmt.Fprintf(&b, "; top-up ceiling %d", s.TopUpTo)
		}
		if s.Steer > 0 {
			fmt.Fprintf(&b, "; steering %d", s.Steer)
		}
	}
	if p.Rates != nil {
		fmt.Fprintf(&b, "\n  fault probability default %g", p.Rates.Default)
		keys := make([]string, 0, len(p.Rates.Kinds))
		for k := range p.Rates.Kinds {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "\n    %s: %g", k, p.Rates.Kinds[k])
		}
	} else if p.Chooser == "navigation" {
		b.WriteString("\n  fixed choice prefix; nominal continuation; no seeded fault draws")
	} else if p.Chooser == "forced-schedule" {
		b.WriteString("\n  one forced schedule; no sampled fault rates")
	} else if p.Exhaust == nil {
		b.WriteString("\n  fault rates unknown (custom chooser)")
	}
	if p.Exhaust != nil {
		e := p.Exhaust
		fmt.Fprintf(&b, "\n  search delays %d; faults %d; max runs %d; partial %t", e.Delays, e.Faults, e.MaxRuns, e.Partial)
		if len(e.Without) > 0 {
			kinds := slices.Clone(e.Without)
			slices.Sort(kinds)
			fmt.Fprintf(&b, "\n  excluded fault kinds: %s", strings.Join(kinds, ", "))
		}
	}
	if len(p.Without) > 0 {
		kinds := slices.Clone(p.Without)
		slices.Sort(kinds)
		fmt.Fprintf(&b, "\n  nominal fault filter: %s", strings.Join(kinds, ", "))
		if p.Chooser == "custom" {
			b.WriteString("\n    custom scripted faults can bypass this filter")
		}
	}
	if p.Validation.Binding == "" {
		b.WriteString("\n  no primary model binding")
	} else {
		fmt.Fprintf(&b, "\n  primary binding %s", p.Validation.Binding)
		keys := make([]string, 0, len(p.Validation.Constants))
		for k := range p.Validation.Constants {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "\n    constant %s = %s", k, p.Validation.Constants[k])
		}
		invariants := slices.Clone(p.Validation.Invariants)
		slices.Sort(invariants)
		if p.Validation.Invariants == nil {
			b.WriteString("\n    invariants: not resolved (model not loaded)")
		} else if len(invariants) == 0 {
			b.WriteString("\n    invariants: none")
		} else {
			fmt.Fprintf(&b, "\n    invariants: %s", strings.Join(invariants, ", "))
		}
		fmt.Fprintf(&b, "\n  validation quint %s; backend %s; evaluator %s", p.Validation.Quint, p.Validation.Backend, p.Validation.Eval)
		if p.Validation.ChoiceLimit > 0 {
			fmt.Fprintf(&b, "\n  finite model-choice limit %d expanded states per actor", p.Validation.ChoiceLimit)
		}
		fmt.Fprintf(&b, "\n  configured evaluator confirmation rate %g; sample seed %q", p.Validation.ConfirmationRate, p.Validation.ConfirmationSeed)
		if p.Validation.Eval == "go" && p.Validation.ConfirmationRate > 0 {
			b.WriteString("\n    unconfirmed actions use at least a one-in-five confirmation rate")
		}
	}
	if s := p.Validation.Search; s != nil {
		fmt.Fprintf(&b, "\n  history search linearize %t; resolve pending %t; clock skew %s", s.Linearize, s.ResolvePending, s.ClockSkew)
		if s.OrderBudget > 0 {
			fmt.Fprintf(&b, "\n    order budget %d", s.OrderBudget)
		}
		if s.ResolveBudget > 0 {
			fmt.Fprintf(&b, "\n    resolve budget %d", s.ResolveBudget)
		}
		if s.Key != "" {
			fmt.Fprintf(&b, "\n    partition key %s", s.Key)
		}
	}
	fmt.Fprintf(&b, "\n  simulation defaults: self-checks %t; all fault families %t", p.Check, p.AllFaults)
	if p.Corpus != "" {
		fmt.Fprintf(&b, "\n  corpus %s", p.Corpus)
	}
	return b.String()
}

// Clone returns a deep copy, so consumers can annotate exported data without
// changing the configuration that execution and other outputs retain.
func (p Profile) Clone() Profile {
	p.Without = slices.Clone(p.Without)
	p.Validation.Constants = maps.Clone(p.Validation.Constants)
	p.Validation.Invariants = slices.Clone(p.Validation.Invariants)
	if p.Seeds != nil {
		v := *p.Seeds
		p.Seeds = &v
	}
	if p.Rates != nil {
		v := *p.Rates
		v.Kinds = maps.Clone(v.Kinds)
		p.Rates = &v
	}
	if p.Exhaust != nil {
		v := *p.Exhaust
		v.Without = slices.Clone(v.Without)
		p.Exhaust = &v
	}
	if p.Validation.Search != nil {
		v := *p.Validation.Search
		p.Validation.Search = &v
	}
	return p
}
