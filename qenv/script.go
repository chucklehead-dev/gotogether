package qenv

import (
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/chucklehead-dev/gotogether/qenv/internal/choice"
	"github.com/chucklehead-dev/gotogether/qobs"
)

// A Scripted fault is one fault a Script makes happen: at the first choice
// of Kind (as reports print it, "rpc.payments.response"; "nemesis.<name>"
// for a nemesis such as "nemesis.crash.payments") once the run has taken
// After steps, the
// script's earlier faults of the same kind have happened, and so have the
// ones it Follows.
type Scripted struct {
	Kind string
	// Option is the fault choice's option; Name, if set, names it instead
	// (a declared kind's, Faults). Ignored for a nemesis.
	Option int
	Name   string
	After  int
	// Follows are the indices (in Script.Faults) of faults of other kinds
	// that must happen first: "a, then b" when the order matters. Faults
	// of different kinds are otherwise independent, so one whose phase has
	// passed blocks no other.
	Follows []int
}

func (s Scripted) String() string {
	if s.Name != "" {
		return fmt.Sprintf("%s=%s after %d steps", s.Kind, s.Name, s.After)
	}
	return fmt.Sprintf("%s=%d after %d steps", s.Kind, s.Option, s.After)
}

// Script is a Chooser that makes exactly the faults it is given, in their
// partial order (each kind's in order, others as Follows says), and no
// others: a model trace's crashes and failures imposed on the program
// (qtest.FaultScript), while the program does the rest. Every
// other fault choice is nominal, and non-fault choices are Base's (nil:
// nominal, the canonical schedule).
//
// Progress tells how many steps the run has taken (nil: its step
// recorder's Count, found through the run): a scripted fault waits for the
// steps before it in the trace, so it lands where the trace put it. A fault the run never comes
// to stays in Remaining, and the run did not follow the trace.
type Script struct {
	Faults   []Scripted
	Base     Chooser
	Progress func() int

	mu   sync.Mutex
	done []bool
}

// due returns the fault of kind that may happen now, or -1: the first of
// its kind not done, if what it follows is done and its steps are taken.
func (s *Script) due(kind string) int {
	if s.done == nil {
		s.done = make([]bool, len(s.Faults))
	}
	for i, f := range s.Faults {
		if f.Kind != kind || s.done[i] {
			continue // a misnamed fault matches no kind: Misnamed says so
		}
		for _, j := range f.Follows {
			if j < 0 || j >= len(s.Faults) || j == i || !s.done[j] {
				return -1 // a follow of no other fault never happens: Misnamed
			}
		}
		if f.After > 0 && s.progress() < f.After {
			return -1
		}
		return i
	}
	return -1
}

func (s *Script) progress() int {
	if s.Progress != nil {
		return s.Progress()
	}
	if r := qobs.From(nil); r != nil {
		return r.Count()
	}
	return 1 << 30 // no recorder to wait for
}

// Choose implements Chooser.
func (s *Script) Choose(kind string, n int) int {
	if !IsFault(kind) {
		if s.Base == nil {
			return 0
		}
		return s.Base.Choose(kind, n)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := strings.TrimPrefix(kind, "fault.")
	i := s.due(k)
	if i < 0 {
		return 0
	}
	f := s.Faults[i]
	v := f.Option
	if f.Name != "" {
		v = slices.Index(choice.Options(k), f.Name) + 1
	}
	if v <= 0 {
		return 0 // no option, or the nominal one: Misnamed says so, and it stays Remaining
	}
	if v >= n {
		return 0 // this choice does not offer the option (a read of a kind whose write can lose its response): wait for one that does
	}
	s.done[i] = true
	return v
}

// ChooseNemesis picks a nemesis by name among the enabled ones: the
// scripted one if it is due, else none (0); the scheduler asks it instead
// of choosing in each family ("fault.nemesis.crash") by index.
func (s *Script) ChooseNemesis(enabled []string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, name := range enabled {
		if j := s.due("nemesis." + name); j >= 0 {
			s.done[j] = true
			return i + 1
		}
	}
	return 0
}

// Entropy implements Entropy: Base's, else zeros (the nominal schedule).
func (s *Script) Entropy(n int) []byte {
	if e := EntropyOf(s.Base); e != nil {
		return e(n)
	}
	return make([]byte, n)
}

// Misnamed says what is wrong with each scripted fault that cannot
// happen as written: a kind no double or fault point declared (a Script
// matches kinds exactly; "rpc.payments" is no kind, "nemesis.crash" no
// nemesis), the "fault." prefix, an option its kind lacks or the nominal
// one, a Follows of no other fault. Ask it after a run: kinds are declared
// as the doubles are made. A misnamed fault never happens and stays in
// Remaining, where it would read as a run that never came to it.
func (s *Script) Misnamed() []string {
	var out []string
	names := choice.Names()
	for i, f := range s.Faults {
		bad := func(format string, a ...any) {
			out = append(out, fmt.Sprintf("script fault %d (%v): ", i, f)+fmt.Sprintf(format, a...))
		}
		for _, j := range f.Follows {
			if j < 0 || j >= len(s.Faults) || j == i {
				bad("follows %d, which is not another fault of the script", j)
			}
		}
		switch {
		case strings.HasPrefix(f.Kind, "fault."):
			bad("name its kind without the \"fault.\" prefix (%q), as reports print it", strings.TrimPrefix(f.Kind, "fault."))
			continue
		case !choice.Exact(f.Kind):
			msg := fmt.Sprintf("%q is no fault kind or nemesis the system declared, so it never happens", f.Kind)
			family := strings.HasPrefix(f.Kind, "nemesis.") && slices.ContainsFunc(names, func(n string) bool { return strings.HasPrefix(n, f.Kind+".") })
			switch near := choice.Nearest(f.Kind, names); {
			case family:
				// a family's name is chosen, but a Script names one of its
				// nemeses: suggesting the name itself said nothing
				msg = fmt.Sprintf("%q is a nemesis family, not a nemesis: name one of its nemeses (%q, with its process or pair)", f.Kind, f.Kind+".<name>")
			case near != "" && near != f.Kind:
				msg += fmt.Sprintf(" (did you mean %s?)", near)
			}
			bad("%s: name a kind as fault coverage prints it (\"rpc.<service>.response\"), a nemesis as \"nemesis.crash.<process>\"", msg)
			continue
		case strings.HasPrefix(f.Kind, "nemesis."):
			continue // chosen by name: Option and Name do not apply
		}
		opts := choice.Options(f.Kind)
		switch {
		case f.Name != "" && opts != nil && !slices.Contains(opts, f.Name):
			bad("%s has no option %q; its options: %s", f.Kind, f.Name, strings.Join(opts, ", "))
		case f.Name == "" && f.Option <= 0:
			bad("option %d is nominal; script the fault's option, from 1 (or its Name)", f.Option)
		case f.Name == "" && opts != nil && f.Option > len(opts)+1: // a fault point's crash follows its declared options
			bad("%s has options %s; option %d is none of them", f.Kind, strings.Join(opts, ", "), f.Option)
		}
	}
	return out
}

// Remaining are the scripted faults that have not happened.
func (s *Script) Remaining() []Scripted {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Scripted
	for i, f := range s.Faults {
		if s.done == nil || !s.done[i] {
			out = append(out, f)
		}
	}
	return out
}

// taskChooser chooses the task to run by id (PCT): runnable in the order
// of the schedule choice's options.
type taskChooser interface{ ChooseTask(runnable []int) int }

// taskChooserOf returns c's choice of a task by id, through the wrappers,
// or nil.
func taskChooserOf(c Chooser) func(runnable []int) int {
	for c != nil {
		if x, ok := c.(taskChooser); ok {
			return x.ChooseTask
		}
		w, ok := c.(Wrapper)
		if !ok {
			return nil
		}
		c = w.Unwrap()
	}
	return nil
}

// nemesisChooser chooses a nemesis by name (Script).
type nemesisChooser interface{ ChooseNemesis(enabled []string) int }

// nemesisOf returns c's choice of a nemesis by name, through the wrappers
// of this package, or nil.
func nemesisOf(c Chooser) func(enabled []string) int {
	for c != nil {
		if x, ok := c.(nemesisChooser); ok {
			return x.ChooseNemesis
		}
		w, ok := c.(Wrapper)
		if !ok {
			return nil
		}
		c = w.Unwrap()
	}
	return nil
}

// Unwrap implements Wrapper: a Script keeps its Base's context.
func (s *Script) Unwrap() Chooser { return s.Base }
