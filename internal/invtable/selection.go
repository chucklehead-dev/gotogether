package invtable

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/chucklehead-dev/gotogether/internal/nearest"
)

// lookup is the boolean val name means as the main module sees it, or an
// error naming the model's boolean vals and the nearest one (flag is how
// the message names where the name came from: "-props").
func (m *Model) lookup(flag, name string) (*Property, error) {
	if p := m.all[m.visible[m.Main][name]]; p != nil {
		return p, nil
	}
	var names []string
	for _, p := range m.all {
		names = append(names, p.Name)
	}
	slices.Sort(names)
	what := "boolean val"
	if flag == "-exclude" {
		what = "boolean val or action"
	}
	msg := fmt.Sprintf("%s names %s, which is no %s of model %s: ", flag, name, what, m.Main)
	if s := nearest.Name(name, names); s != "" {
		msg += fmt.Sprintf("did you mean %s? ", s)
	}
	return nil, fmt.Errorf("%sname one of its boolean vals (%s)", msg, strings.Join(names, ", "))
}

// Only makes names, in this order, the table's properties, whatever a
// binding checks: -props.
func (m *Model) Only(flag string, names []string) error {
	var ps []*Property
	for _, n := range names {
		p, err := m.lookup(flag, n)
		if err != nil {
			return err
		}
		if !slices.Contains(ps, p) {
			ps = append(ps, p)
		}
	}
	m.keep(ps)
	return nil
}

// Exclude leaves names out of the tables: -exclude. A boolean val leaves
// the properties, an action the actions' rows (it stays an action, which
// code may name). A name that is neither is an error; one the selection
// had left out already is none.
func (m *Model) Exclude(names []string) error {
	var ps []*Property
	drop := map[*Property]bool{}
	for _, n := range names {
		if m.actionsAll[n] {
			m.Actions = slices.DeleteFunc(m.Actions, func(a *Property) bool { return a.Name == n })
			continue
		}
		p, err := m.lookup("-exclude", n)
		if err != nil {
			return err
		}
		drop[p] = true
	}
	for _, p := range m.Properties {
		if !drop[p] {
			ps = append(ps, p)
		}
	}
	m.keep(ps)
	return nil
}

// irExpr is as much of an IR expression as Conjuncts reads.
type irExpr struct {
	Kind   string            `json:"kind"`
	Opcode string            `json:"opcode"`
	Name   string            `json:"name"`
	Args   []json.RawMessage `json:"args"`
}

// Conjuncts are the properties a val that is a conjunction stands for
// (-invariant safe, where `val safe = and { noOverdraft, conserved }`):
// its conjuncts that are boolean vals, each expanded the same way in
// turn, depth first, each once. A val that is no conjunction (and, or
// all) is itself; so is one with a conjunct that is no val's name
// (`x != 5`): it stays a row, before its named conjuncts, since no other
// row stands for that conjunct.
func (m *Model) Conjuncts(roots []string) ([]string, error) {
	var out []string
	seen := map[*Property]bool{}
	var expand func(p *Property)
	expand = func(p *Property) {
		if seen[p] {
			return
		}
		seen[p] = true
		var e irExpr
		if json.Unmarshal(p.expr, &e) != nil || e.Kind != "app" || (e.Opcode != "and" && e.Opcode != "actionAll") {
			out = append(out, p.Name)
			return
		}
		var parts []*Property
		whole := false
		for _, raw := range e.Args {
			var a irExpr
			_ = json.Unmarshal(raw, &a)
			q := (*Property)(nil)
			if a.Kind == "name" {
				q = m.all[m.visible[p.Module][a.Name]]
			}
			if q == nil {
				whole = true
				continue
			}
			parts = append(parts, q)
		}
		if whole {
			out = append(out, p.Name)
		}
		for _, q := range parts {
			expand(q)
		}
	}
	for _, r := range roots {
		p, err := m.lookup("-invariant", r)
		if err != nil {
			return nil, err
		}
		expand(p)
	}
	return out, nil
}

// FirstSentence is a doc comment's meaning: its first paragraph's lines
// joined, up to the first period followed by a space or the end.
func FirstSentence(doc string) string {
	var lines []string
	for l := range strings.SplitSeq(strings.TrimSpace(doc), "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			break
		}
		lines = append(lines, l)
	}
	text := strings.Join(lines, " ")
	for i := 0; i < len(text); i++ {
		if text[i] == '.' && (i+1 == len(text) || text[i+1] == ' ') {
			return text[:i+1]
		}
	}
	return text
}
