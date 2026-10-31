// Package replay writes the Quint modules that replay recorded steps: the
// validator's, not a user's (binding plans the calls, validate runs quint
// on what this writes).
package replay

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// Generated is a Quint module that replays one observed trace.
type Generated struct {
	Module   string         // module name
	Text     string         // the .qnt source
	LineStep map[int]int    // source line -> index into Calls
	Calls    []binding.Call // the replayed steps, in order, including inferred ones
	Tests    GeneratedTests // names of the runs in Text
	// Start is the call its runs start from (nil: init), and StartLines
	// the lines it is written at, one per run: a failure there is the
	// start's (a snapshot whose action is disabled, or whose state fails
	// a check)
	Start      *binding.Call
	StartLines map[int]bool
}

// Steps counts the run's steps: its calls, and the snapshot step it
// starts from, if any (a resume's admitted state is none).
func (g *Generated) Steps() int {
	if g.Start != nil && g.Start.Step != nil {
		return len(g.Calls) + 1
	}
	return len(g.Calls)
}

// GeneratedTests names the runs a generated module holds.
type GeneratedTests struct {
	Conforms   string            // replays every step; checks each step's expect
	Invariants map[string]string // invariant -> run that checks it in every state
	Eventually map[string]string // eventual val -> run that checks it in the last state
	// All, when there are invariants, replays the steps once checking each
	// step's expect and every invariant: the fast path. If it passes, so
	// do Conforms and every invariant run; if not, those runs say which
	// check failed, and where.
	All string
}

// Generate builds binding b's replay module for one actor's trace. qntPath is where
// the module will be written (the model import is made relative to it).
func Generate(b *binding.Binding, m *binding.Model, t *qtrace.Trace, qntPath string) (*Generated, error) {
	p, err := b.Plan(m, t)
	if err != nil {
		return nil, err
	}
	return RenderModule(ModuleSpec{
		Spec: b.SpecPath(), Module: b.Module, Constants: p.ConstantList(), Defs: b.Defs, Invariants: b.Invariants, Eventually: b.Eventually,
		Header: []string{fmt.Sprintf("actor %q, %d observed steps, processes %v", t.Actor, len(t.Steps), t.Processes)}, Start: p.Start, StateVars: p.StateVars,
	}, p.Calls, qntPath)
}

// ModuleSpec says what a generated replay module imports and checks.
type ModuleSpec struct {
	StateVars  []string // complete state, compared by noninitial snapshot checkpoints
	Spec       string   // the model file
	Module     string   // parameterised module, instantiated with Constants
	Constants  []string // "NAME = expr"
	Instance   string   // if set, import this (already instantiated) module instead
	Defs       string   // Quint helpers
	Invariants []string
	Eventually []string // vals checked in the last state only (liveness obligations)
	InitExpect string   // checked in the initial state, if set
	Header     []string // comment lines
	Tests      TestSet  // which runs to write (default: all)
	// Start is the call every run starts from instead of init (a snapshot
	// step's: binding.Plan.Start), its expect checked where the steps'
	// are; Starts, if set, a batch's, one per run (nil: init)
	Start  *binding.Call
	Starts []*binding.Call
}

// TestSet selects the runs of a replay module.
type TestSet int

const (
	// AllTests writes the fused run and the separate ones.
	AllTests TestSet = iota
	// FastTests writes only the fused run (allTest), or only conformsTest
	// when there are no invariants: one replay that decides a passing run.
	FastTests
	// DetailTests writes conformsTest and one run per invariant: they say
	// which check failed, and where.
	DetailTests
)

// segmentSteps is the longest `.then` chain written as one expression.
const segmentSteps = 20

// RenderModule writes the replay module for calls: a conformance run with
// each call's expect, and one run per invariant.
func RenderModule(ms ModuleSpec, calls []binding.Call, qntPath string) (*Generated, error) {
	gs, err := RenderBatch(ms, [][]binding.Call{calls}, qntPath)
	if err != nil {
		return nil, err
	}
	return gs[0], nil
}

// RenderBatch writes one module replaying several runs of the same
// instance (same ModuleSpec): the runs of run i are prefixed r<i>_, so one
// `quint test` process checks them all. Each returned Generated describes
// one run; all share Text. With a single run there is no prefix.
func RenderBatch(ms ModuleSpec, runs [][]binding.Call, qntPath string) ([]*Generated, error) {
	for _, calls := range runs {
		for _, call := range calls {
			if call.Checkpoint && len(ms.StateVars) == 0 {
				return nil, fmt.Errorf("snapshot checkpoint %s: replay needs the model's complete state variables", call.Action)
			}
		}
	}
	rel, err := filepath.Rel(filepath.Dir(qntPath), strings.TrimSuffix(ms.Spec, ".qnt"))
	if err != nil {
		return nil, err
	}
	var w strings.Builder
	line := 1
	pr := func(format string, a ...any) {
		s := fmt.Sprintf(format, a...)
		w.WriteString(s)
		w.WriteString("\n")
		line += strings.Count(s, "\n") + 1
	}
	pr("// Generated by gotogether from observed telemetry. Do not edit.")
	for _, h := range ms.Header {
		pr("// %s", h)
	}
	if len(runs) > 1 {
		pr("// %d runs, checked together: the tests of run i are r<i>_*.", len(runs))
	}
	pr("module observed {")
	if ms.Instance != "" {
		pr("  import %s.* from %q", ms.Instance, rel)
	} else if len(ms.Constants) == 0 {
		pr("  import %s.* from %q", ms.Module, rel)
	} else {
		pr("  import %s(%s).* from %q", ms.Module, strings.Join(ms.Constants, ", "), rel)
	}
	if strings.TrimSpace(ms.Defs) != "" {
		pr("%s", indent(strings.TrimRight(ms.Defs, "\n"), "  "))
	}
	out := make([]*Generated, len(runs))
	for ri, calls := range runs {
		start := ms.Start
		if ms.Starts != nil {
			start = ms.Starts[ri]
		}
		g := &Generated{Module: "observed", LineStep: map[int]int{}, Calls: calls, Start: start, StartLines: map[int]bool{}}
		out[ri] = g
		// begin is the run's first state, with checks of it (and the
		// snapshot's own expect, if checked): init, or the start's call
		begin := func(checks ...string) string {
			first := "init"
			if start != nil {
				first = start.Quint
			}
			var cs []string
			for _, c := range checks {
				if c != "" {
					cs = append(cs, c)
				}
			}
			switch len(cs) {
			case 0:
				return first
			case 1:
				return fmt.Sprintf("%s.expect(%s)", first, cs[0])
			}
			return fmt.Sprintf("%s.expect((%s) and %s)", first, cs[0], strings.Join(cs[1:], " and "))
		}
		startExpect := ""
		if start != nil {
			startExpect = start.Expect
		}
		prefix := ""
		if len(runs) > 1 {
			prefix = fmt.Sprintf("r%d_", ri)
			pr("")
			pr("  // ---- run %d ----", ri)
		}
		pr("")
		// chain writes a run of init and then every call, rendered by step.
		// Long chains are split into named segments, and long chains of
		// segments into segments of segments: Quint's Rust evaluator rejects
		// a `.then` chain nested much past 50 steps, so no chain is longer
		// than segmentSteps.
		chain := func(name, first string, step func(c binding.Call) string, comment bool) {
			type item struct {
				call int    // index of the call, or -1 for a segment
				ref  string // the segment's name
				last int    // the last call it covers
			}
			// emit writes an item on its own line (quint's error positions map
			// back to the observed step by line): `.then(...)`, or bare as
			// the first item of a segment. A segment that evaluates to false
			// is reported at the line that uses it: its last call was the
			// disabled one (an earlier one would be reported inside it).
			emit := func(it item, then bool) {
				text := it.ref
				if it.call >= 0 {
					if comment {
						// a comment is one line: a recorded newline (a
						// string's, which Quint writes as it is) would end it
						pr("    // step %d: %s", it.call+1, oneLine(calls[it.call].String()))
					}
					c := calls[it.call]
					if c.Checkpoint {
						c.Quint = checkpoint(c.Quint, ms.StateVars)
					}
					text = step(c)
				}
				g.LineStep[line] = it.last
				if then {
					pr("    .then(%s)", text)
				} else {
					pr("    %s", text)
				}
			}
			items := make([]item, len(calls))
			for i := range calls {
				items[i] = item{call: i, last: i}
			}
			for level := 0; len(items) > segmentSteps; level++ {
				var next []item
				for s := 0; s < len(items); s += segmentSteps {
					// no "Test" in the name: quint test --match would take it for a test
					base := strings.TrimSuffix(name, "Test")
					seg := fmt.Sprintf("%s_seg%d", base, s/segmentSteps)
					if level > 0 {
						seg = fmt.Sprintf("%s_l%d_%d", base, level, s/segmentSteps)
					}
					group := items[s:min(s+segmentSteps, len(items))]
					pr("  action %s =", seg)
					for j, it := range group {
						emit(it, j > 0)
					}
					next = append(next, item{call: -1, ref: seg, last: group[len(group)-1].last})
				}
				items = next
			}
			pr("  run %s =", name)
			g.StartLines[line] = true
			pr("    %s", first)
			for _, it := range items {
				emit(it, true)
			}
		}
		fused := len(ms.Invariants) > 0 && ms.Tests != DetailTests
		separate := ms.Tests != FastTests || len(ms.Invariants) == 0
		if separate {
			g.Tests.Conforms = prefix + "conformsTest"
			pr("  // The observed steps, each checked against the observed state.")
			chain(g.Tests.Conforms, begin(startExpect, ms.InitExpect), func(c binding.Call) string {
				if c.Expect != "" {
					return fmt.Sprintf("%s.expect(%s)", c.Quint, c.Expect)
				}
				return c.Quint
			}, true)
		}
		if fused {
			g.Tests.All = prefix + "allTest"
			all := strings.Join(ms.Invariants, " and ")
			pr("")
			pr("  // The steps once, with every check: each step's expect and every invariant.")
			chain(g.Tests.All, begin(startExpect, ms.InitExpect, all), func(c binding.Call) string {
				if c.Expect != "" {
					return fmt.Sprintf("%s.expect((%s) and %s)", c.Quint, c.Expect, all)
				}
				return fmt.Sprintf("%s.expect(%s)", c.Quint, all)
			}, false)
		}
		// each eventual val in its own run, in every test set: it holds or
		// not at the end only, which the fused run cannot say
		g.Tests.Eventually = map[string]string{}
		for _, ev := range ms.Eventually {
			name := prefix + ev + "EvTest"
			g.Tests.Eventually[ev] = name
			pr("")
			pr("  // The same steps; %s must hold when they end.", ev)
			if len(calls) == 0 {
				pr("  run %s =", name)
				g.StartLines[line] = true
				pr("    %s", begin(ev))
				continue
			}
			n := 0
			chain(name, begin(), func(c binding.Call) string {
				if n++; n == len(calls) {
					return fmt.Sprintf("%s.expect(%s)", c.Quint, ev)
				}
				return c.Quint
			}, false)
		}
		g.Tests.Invariants = map[string]string{}
		if separate {
			for _, inv := range ms.Invariants {
				name := prefix + inv + "InvTest"
				g.Tests.Invariants[inv] = name
				pr("")
				pr("  // The same steps; %s must hold in every state.", inv)
				chain(name, begin(inv), func(c binding.Call) string {
					return fmt.Sprintf("%s.expect(%s)", c.Quint, inv)
				}, false)
			}
		}
	}
	pr("}")
	for _, g := range out {
		g.Text = w.String()
	}
	return out, nil
}

// Spec is what a replay of plan p with binding b imports and checks.
func Spec(b *binding.Binding, p *binding.Plan) ModuleSpec {
	return ModuleSpec{Spec: b.SpecPath(), Module: b.Module, Constants: p.ConstantList(), Defs: b.Defs, Invariants: b.Invariants, Eventually: b.Eventually,
		Header: []string{fmt.Sprintf("actor %q, %d observed steps, processes %v", p.Actor, p.ObservedSteps, p.Processes)}, Start: p.Start, StateVars: p.StateVars}
}

func indent(s, pfx string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if l != "" {
			lines[i] = pfx + l
		}
	}
	return strings.Join(lines, "\n")
}

// oneLine is s with its line breaks written as \n and \r, for a comment.
func oneLine(s string) string { return strings.NewReplacer("\n", `\n`, "\r", `\r`).Replace(s) }

// checkpoint captures state eagerly: a val would be reevaluated after the
// snapshot and make this comparison tautological. The singleton choice has
// exactly one outcome. A successful snapshot is a stutter; a mismatch fails.
func checkpoint(action string, vars []string) string {
	var fields []string
	for i, v := range vars {
		fields = append(fields, fmt.Sprintf("v%d: %s", i, v))
	}
	state := "{ " + strings.Join(fields, ", ") + " }"
	name := "__gtg_checkpoint_before"
	for strings.Contains(action+state, name) {
		name += "_"
	}
	return fmt.Sprintf("{ nondet %s = Set(%s).oneOf() %s.expect(%s == %s) }", name, state, action, state, name)
}
