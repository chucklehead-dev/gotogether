package binding

import (
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// A Possible is something the model must be able to do (Simulation's
// possible): some trace of the model on its own reaches a state where
// When holds, or takes each of Steps in turn (other steps between them).
// A model that can do neither is too strict for a conforming run ever to
// show it: checking the code against the model would then prove nothing
// of it. In the binding it is a string, a Quint predicate over the
// model's state or a model action's name (that action is taken), or a
// mapping of a name and when or steps:
//
//	possible:
//	  - 'balance > 0'
//	  - withdraw
//	  - name: overdraft refused
//	    steps: [deposit, 'withdraw(result = "insufficient")']
type Possible struct {
	// Name is how reports name it ("" for its text)
	Name string `yaml:"name"`
	// When is a Quint predicate over the model's state
	When string `yaml:"when"`
	// Steps are the model actions some trace takes in this order, each a
	// name or a call naming some of its parameters' values as Quint
	// literals: withdraw(result = "insufficient")
	Steps []string `yaml:"steps"`
	// bare: written as a string, which may name an action
	bare bool
}

// UnmarshalYAML reads a string as a predicate (When) or, if it is a
// name alone, a step; a mapping as Name with When or Steps. Which a name
// is (an action, or a val the model defines) Resolve decides.
func (p *Possible) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		*p = Possible{When: strings.TrimSpace(n.Value), bare: true}
		return nil
	}
	type plain Possible
	var q plain
	if err := n.Decode(&q); err != nil {
		return err
	}
	*p = Possible(q)
	return nil
}

// Text is the possible as a report names it: its name, else its
// predicate, else its steps joined by " then ".
func (p Possible) Text() string {
	switch {
	case p.Name != "":
		return p.Name
	case p.When != "":
		return p.When
	}
	return strings.Join(p.Steps, " then ")
}

// The pattern of a name alone (read-only).
var identifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// Resolve is p against m: a predicate's text when it is a state
// predicate, else its steps. A string that is a name alone is a step if
// it names one of m's actions, a predicate otherwise (a val). An error
// says what is wrong: both or neither of when and steps, a step naming
// no action or a parameter the action lacks.
func (p Possible) Resolve(m *Model) (when string, steps []StepPattern, err error) {
	if (p.When == "") == (len(p.Steps) == 0) {
		return "", nil, fmt.Errorf("possible %q: give when (a Quint predicate over the state) or steps (the actions some trace takes in turn), one of them", p.Text())
	}
	if p.When != "" {
		if _, ok := m.Actions[p.When]; ok && p.bare && identifier.MatchString(p.When) {
			steps = []StepPattern{{Action: p.When, Text: p.When}}
			return "", steps, nil
		}
		return p.When, nil, nil
	}
	for _, s := range p.Steps {
		sp, err := ParseStepPattern(s)
		if err != nil {
			return "", nil, fmt.Errorf("possible %q: %w", p.Text(), err)
		}
		ps, ok := m.Actions[sp.Action]
		if !ok {
			return "", nil, fmt.Errorf("possible %q: step %s: %s", p.Text(), s, m.notAnAction(sp.Action))
		}
		for _, name := range sp.Params {
			found := false
			for _, q := range ps {
				found = found || q.Name == name
			}
			if !found {
				return "", nil, fmt.Errorf("possible %q: step %s: %s has no parameter %s; signature %s", p.Text(), s, sp.Action, name, m.Signature(sp.Action))
			}
		}
		steps = append(steps, sp)
	}
	return "", steps, nil
}

// A StepPattern is a step a possible's trace takes: an action, and the
// values some of its parameters take (Quint literals, by parameter), in
// the order written.
type StepPattern struct {
	Action string
	Params []string
	Values []string
	// Text is the pattern as written
	Text string
}

// ParseStepPattern reads `name` or `name(p = literal, ...)`.
func ParseStepPattern(s string) (StepPattern, error) {
	s = strings.TrimSpace(s)
	sp := StepPattern{Text: s}
	name, rest, call := strings.Cut(s, "(")
	sp.Action = strings.TrimSpace(name)
	if !identifier.MatchString(sp.Action) {
		return sp, fmt.Errorf("step %q: an action's name, or name(param = literal, ...)", s)
	}
	if !call {
		return sp, nil
	}
	rest = strings.TrimSpace(rest)
	if !strings.HasSuffix(rest, ")") {
		return sp, fmt.Errorf("step %q: no closing parenthesis", s)
	}
	for _, part := range splitTopLevel(strings.TrimSuffix(rest, ")")) {
		k, v, ok := strings.Cut(part, "=")
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if !ok || !identifier.MatchString(k) || v == "" {
			return sp, fmt.Errorf("step %q: %q is no param = literal", s, part)
		}
		sp.Params, sp.Values = append(sp.Params, k), append(sp.Values, v)
	}
	return sp, nil
}

// splitTopLevel splits s at the commas outside brackets and string
// literals.
func splitTopLevel(s string) []string {
	var out []string
	depth, inStr, start := 0, false, 0
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case inStr && c == '\\':
			i++
		case c == '"':
			inStr = !inStr
		case inStr:
		case c == '(' || c == '[' || c == '{':
			depth++
		case c == ')' || c == ']' || c == '}':
			depth--
		case c == ',' && depth == 0:
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if strings.TrimSpace(s[start:]) != "" {
		out = append(out, s[start:])
	}
	return out
}
