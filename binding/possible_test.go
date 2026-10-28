package binding

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// A possible reads as a string (a predicate, or an action's name) or a
// mapping; Resolve tells a name that is an action from a val, and
// refuses a step naming no action or parameter of the model.
func TestPossibleResolve(t *testing.T) {
	var ps []Possible
	src := `
- 'sold > 2'
- refund
- soldOut
- name: sold out
  steps: [insert, 'buy(result = "ok", note = Set(1, 2))']
- name: both
  when: 'x'
  steps: [refund]
- name: unknown
  steps: [rfund]
- name: bad param
  steps: ['buy(reslt = "ok")']
`
	if err := yaml.Unmarshal([]byte(src), &ps); err != nil {
		t.Fatal(err)
	}
	m := &Model{Module: "vending", Actions: map[string][]Param{"insert": {{Name: "coin"}}, "buy": {{Name: "result"}, {Name: "note"}}, "refund": nil},
		Vals: map[string]bool{"soldOut": true}}
	type want struct {
		when, steps, err string
	}
	wants := []want{{when: "sold > 2"}, {steps: "refund"}, {when: "soldOut"},
		{steps: `insert|buy:result="ok",note=Set(1, 2)`}, {err: "one of them"}, {err: "rfund is not an action"}, {err: "no parameter reslt"}}
	for i, p := range ps {
		when, steps, err := p.Resolve(m)
		w := wants[i]
		if w.err != "" {
			if err == nil || !strings.Contains(err.Error(), w.err) {
				t.Errorf("%s: %v, want %q", p.Text(), err, w.err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", p.Text(), err)
			continue
		}
		var parts []string
		for _, s := range steps {
			x := s.Action
			for j := range s.Params {
				if j == 0 {
					x += ":"
				} else {
					x += ","
				}
				x += s.Params[j] + "=" + s.Values[j]
			}
			parts = append(parts, x)
		}
		if when != w.when || strings.Join(parts, "|") != w.steps {
			t.Errorf("%s: when %q steps %q, want %q %q", p.Text(), when, strings.Join(parts, "|"), w.when, w.steps)
		}
	}
}
