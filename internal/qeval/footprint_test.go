package qeval

import (
	"slices"
	"strings"
	"testing"
)

// A footprint records each entry a step or a judged val reads and
// writes, and the first access that is not to one entry: what
// validate's partition checks a model's factoring by.
func TestFootprint(t *testing.T) {
	m := compile(t, "footprint.qnt", "footprint", "init", "step")
	consts := map[string]Value{}
	s0, err := m.Init(consts, "init")
	if err != nil {
		t.Fatal(err)
	}
	e := func(v, k string) Entry { return Entry{v, `"` + k + `"`} }
	for _, c := range []struct {
		name          string
		args          []Value
		judged        bool
		reads, writes []Entry
		whole         string // a substring of Whole ("" none)
	}{
		{name: "deposit", args: []Value{Str("a"), Int(1)}, reads: []Entry{e("bal", "a"), e("bal", "a")}, writes: []Entry{e("bal", "a")}},
		{name: "open", args: []Value{Str("c"), Str("k")}, reads: []Entry{e("bal", "c")}, writes: []Entry{e("bal", "c"), e("idx", "k")}},
		{name: "peek", args: []Value{Str("b")}, reads: []Entry{e("bal", "b")}},
		{name: "move", args: []Value{Str("a"), Str("b")}, reads: []Entry{e("bal", "a"), e("bal", "a"), e("bal", "b"), e("bal", "b")}, writes: []Entry{e("bal", "a"), e("bal", "b")}},
		{name: "scan", args: []Value{Int(2)}, whole: "bal read whole (keys)"},
		{name: "count", whole: "bal read whole (keys)"},
		{name: "tally", whole: "reads total"},
		{name: "reset", whole: "bal assigned anew"},
		{name: "same", args: []Value{Str("a")}, writes: []Entry{e("bal", "a")}, whole: "bal read whole (eq)"},
		{name: "positive", judged: true, reads: []Entry{e("bal", "a"), e("bal", "b")}},
		{name: "both", judged: true, reads: []Entry{e("bal", "a"), e("bal", "b")}},
		{name: "notAll", judged: true, reads: []Entry{e("bal", "a")}, whole: "not each entry's"},
		{name: "pairs", judged: true, whole: "not each entry's"},
		{name: "usesPositive", judged: true, whole: "not each entry's"},
		{name: "cachedThenOr", judged: true, whole: "used where it is not each entry's"},
		{name: "positive", whole: "not each entry's"}, // a step's, not judged
	} {
		t.Run(c.name, func(t *testing.T) {
			fp := &Footprint{Judging: c.judged}
			tm := m.Tracking(fp)
			var err error
			if c.judged || c.name == "positive" {
				_, err = tm.Eval(consts, s0, c.name)
			} else {
				_, _, err = tm.Step(consts, s0, c.name, c.args...)
			}
			if err != nil {
				t.Fatal(err)
			}
			if c.whole == "" && fp.Whole != "" || c.whole != "" && !strings.Contains(fp.Whole, c.whole) {
				t.Errorf("Whole = %q, want %q", fp.Whole, c.whole)
			}
			if c.whole == "" {
				if !slices.Equal(fp.Reads, c.reads) || !slices.Equal(fp.Writes, c.writes) {
					t.Errorf("reads %v writes %v, want %v and %v", fp.Reads, fp.Writes, c.reads, c.writes)
				}
			}
			// the same values as an evaluation that tracks nothing
			if c.judged {
				a, _ := m.Eval(consts, s0, c.name)
				b, _ := tm.Eval(consts, s0, c.name)
				if Compare(a, b) != 0 {
					t.Errorf("tracked %s = %s, untracked %s", c.name, String(b), String(a))
				}
			} else if c.name != "positive" {
				a, okA, _ := m.Step(consts, s0, c.name, c.args...)
				b, okB, _ := tm.Step(consts, s0, c.name, c.args...)
				if okA != okB || okA && !sameState(a, b) {
					t.Errorf("tracked step %v %v, untracked %v %v", b, okB, a, okA)
				}
			}
		})
	}
}
