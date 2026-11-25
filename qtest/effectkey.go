package qtest

import (
	"text/template/parse"

	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// An effectBlind keys runs for a binding that compares no effects (no
// effects section, no action naming an effects def) as they are without
// their effect records (I/O specifications, DESIGN §112): a double
// records every send, at its own place in its recorder's seqs, so two
// runs of the same model steps interleave their sends differently, and a
// key with the effect records in it made every such run a quint replay
// of its own (twophase's early-commit mutant: 60 replays where 47 were
// before the doubles recorded effects). The verdict of such a binding
// reads no effect record. It does read seqs (a gap is a lost step), so a
// step's seq is renumbered past the effect records of its recorder before
// it: a seq no record has is still a gap.
//
// Where anything else could read an effect record or a raw seq, a run is
// keyed whole, as before: a template that reads a step's seq (.seq,
// .step.seq, the whole step), diagnostic evidence's origins, a lost tail
// (its cut is a seq), a pending invocation (its report keeps the effects
// it made), steps without seqs.
type effectBlind struct{ on bool }

func newEffectBlind(cc *validate.Checker) effectBlind {
	b, m := cc.Binding, cc.Model
	if b == nil || m == nil || len(b.DiagnosticEvidence) > 0 || b.Effects != nil && len(b.Effects.Kinds) > 0 {
		return effectBlind{}
	}
	for name, ps := range m.Actions {
		a := b.Actions[name]
		if a.Effects != "" && a.Effects != "-" {
			return effectBlind{}
		}
		if readsSeq(a.Expect) {
			return effectBlind{}
		}
		for _, p := range ps {
			if readsSeq(a.Template(p)) {
				return effectBlind{}
			}
		}
	}
	for _, a := range b.Actions { // an action the model lacks fails the plan; its templates are no key's
		for _, t := range a.Args {
			if readsSeq(t) {
				return effectBlind{}
			}
		}
	}
	return effectBlind{on: true}
}

// view is steps as the key sees them (renumbered, for the key) and as a
// verdict is rebased between runs of one key (the steps without their
// effect records, their seqs their own), or steps twice when the run is
// keyed whole.
func (e effectBlind) view(steps []qtrace.Step) (key, rebase []qtrace.Step) {
	if !e.on || !blindable(steps) {
		return steps, steps
	}
	type rs struct {
		rec string
		seq uint64
	}
	// how many effect records each recorder made before a seq
	var effects []rs
	for _, s := range steps {
		if s.Effect != "" {
			effects = append(effects, rs{s.Recorder, s.Seq})
		}
	}
	if len(effects) == 0 {
		return steps, steps
	}
	before := func(rec string, seq uint64) uint64 {
		var n uint64
		for _, x := range effects {
			if x.rec == rec && x.seq < seq {
				n++
			}
		}
		return n
	}
	key = make([]qtrace.Step, 0, len(steps)-len(effects))
	rebase = make([]qtrace.Step, 0, len(steps)-len(effects))
	for _, s := range steps {
		if s.Effect != "" {
			continue
		}
		rebase = append(rebase, s)
		k := s
		k.Seq -= before(s.Recorder, s.Seq)
		if k.Call != 0 {
			k.Call -= before(s.Recorder, s.Call)
		}
		k.Made, k.EffectsObserved = nil, false
		key = append(key, k)
	}
	return key, rebase
}

// blindable reports whether steps can be keyed without their effect
// records: every step has a seq, no tail is lost, every invocation is
// completed.
func blindable(steps []qtrace.Step) bool {
	type rs struct {
		rec string
		seq uint64
	}
	completed := map[rs]bool{}
	for _, s := range steps {
		if s.Seq == 0 || s.Lost != "" {
			return false
		}
		if s.Effect == "" && s.Call != 0 {
			completed[rs{s.Recorder, s.Call}] = true
		}
	}
	for _, s := range steps {
		if s.Invocation && !completed[rs{s.Recorder, s.Seq}] {
			return false
		}
	}
	return true
}

// readsSeq reports whether a template could read a step's seq: a field
// or string named seq (.seq, .step.seq, index .step "seq"), the step or
// the data whole (.step, .), or a template that does not parse.
func readsSeq(src string) bool {
	if src == "" {
		return false
	}
	tr := parse.New("")
	tr.Mode = parse.SkipFuncCheck
	trees := map[string]*parse.Tree{}
	if _, err := tr.Parse(src, "", "", trees); err != nil {
		return true
	}
	ids := func(id []string) bool {
		for _, s := range id {
			if s == "seq" {
				return true
			}
		}
		return len(id) == 1 && id[0] == "step"
	}
	var walk func(parse.Node) bool
	walk = func(n parse.Node) bool {
		switch x := n.(type) {
		case nil:
			return false
		case *parse.ListNode:
			if x == nil {
				return false
			}
			for _, n := range x.Nodes {
				if walk(n) {
					return true
				}
			}
		case *parse.ActionNode:
			return walk(x.Pipe)
		case *parse.PipeNode:
			if x == nil {
				return false
			}
			for _, c := range x.Cmds {
				if walk(c) {
					return true
				}
			}
		case *parse.CommandNode:
			for _, a := range x.Args {
				if walk(a) {
					return true
				}
			}
		case *parse.DotNode:
			return true
		case *parse.FieldNode:
			return ids(x.Ident)
		case *parse.VariableNode:
			return len(x.Ident) == 1 || ids(x.Ident[1:]) // $ alone is the data whole
		case *parse.ChainNode:
			return walk(x.Node) || ids(x.Field)
		case *parse.StringNode:
			return x.Text == "seq"
		case *parse.IfNode:
			return walk(x.Pipe) || walk(x.List) || walk(x.ElseList)
		case *parse.RangeNode:
			return walk(x.Pipe) || walk(x.List) || walk(x.ElseList)
		case *parse.WithNode:
			return walk(x.Pipe) || walk(x.List) || walk(x.ElseList)
		case *parse.TemplateNode:
			return true // another template, unseen here
		}
		return false
	}
	for _, t := range trees {
		if walk(t.Root) {
			return true
		}
	}
	return false
}
