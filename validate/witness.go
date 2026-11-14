package validate

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/itf"
)

// WitnessPrefix names an antecedent's witness val: gotogetherNot_<inv> is
// not(<inv>'s antecedent), so a run in which it fails as an invariant met
// the antecedent there.
const WitnessPrefix = "gotogetherNot_"

// AntecedentWitnesses are the witness vals of the invariants named that
// have an antecedent (the binding's antecedents, else the model's `A
// implies B`): their Quint definitions, to append to a binding's Defs,
// and each witness's invariant by its name. qtest's vacuity check judges
// runs against them; a compiled model carries every candidate
// invariant's witness (compiledBinding), so that check needs no quint.
func AntecedentWitnesses(b *binding.Binding, m *binding.Model, invariants []string) (defs string, witnessOf map[string]string) {
	witnessOf = map[string]string{}
	var d strings.Builder
	for _, inv := range slices.Sorted(slices.Values(invariants)) {
		a, ok := b.Antecedents[inv]
		if !ok {
			a, ok = m.Antecedents[inv]
		}
		if !ok {
			continue
		}
		n := WitnessPrefix + inv
		if _, dup := witnessOf[n]; dup {
			continue
		}
		witnessOf[n] = inv
		fmt.Fprintf(&d, "\nval %s = not(%s)", n, a)
	}
	return d.String(), witnessOf
}

// compiledBinding is the binding a compiled model is built from (Compile)
// and checked against (NewCompiledChecker): b with every boolean val of the
// model a candidate invariant, and each candidate's antecedent witness, so
// a check by the compiled model alone can choose any of them (WithInvariants,
// qtest's Invariants and vacuity check) where quint is not installed. The
// checker still judges b's own invariants: candidates are evaluated only
// when chosen.
func compiledBinding(b *binding.Binding, m *binding.Model) *binding.Binding {
	cb := *b
	seen := map[string]bool{}
	var cands []string
	for _, inv := range append(append([]string{}, b.Invariants...), slices.Sorted(maps.Keys(m.Vals))...) {
		if !seen[inv] {
			seen[inv] = true
			cands = append(cands, inv)
		}
	}
	defs, witnessOf := AntecedentWitnesses(b, m, cands)
	cb.Defs = b.Defs + defs
	cb.Invariants = append(cands, slices.Sorted(maps.Keys(witnessOf))...)
	// a constant given as a literal is a parameter, so that a check may
	// give another literal (qtest.Constants): one given as an expression
	// (1.to(3), a def) stays inline, the evaluator reading literals only
	var runtime []string
	for _, c := range slices.Sorted(maps.Keys(b.Constants)) {
		if _, ok := m.Consts[c]; ok && isLiteral(b.Constants[c]) {
			runtime = append(runtime, c)
		}
	}
	return cb.WithRuntimeConstants(runtime...)
}

// isLiteral reports whether s is a Quint literal the Go evaluator reads
// (itf.ParseQuint), not a template the binding computes from the steps.
func isLiteral(s string) bool {
	if strings.Contains(s, "{{") {
		return false
	}
	_, err := itf.ParseQuint(s)
	return err == nil
}
