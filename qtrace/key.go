package qtrace

import "fmt"

// A Keyer renders steps as a verdict cache key, one step at a time: two
// runs whose keys are equal get the same verdict from the same instance.
// Recorders, processes and threads are numbered by first appearance, so
// runs that differ only in their generated ids (an incarnation's process
// name, a span id) share a key. Everything else a verdict can depend on
// is in it: the sequence numbers (gaps; a template's .seq), the error, and
// the times when the step has an invocation time (real-time order), not
// otherwise, so runs that differ only in their virtual delays share one.
type Keyer struct{ ids map[string]int }

// Step is the key of the next step.
func (k *Keyer) Step(s *Step) string {
	var times string
	if !s.Invoked.IsZero() {
		times = fmt.Sprintf("|%d-%d", s.Invoked.UnixNano(), s.Time.UnixNano())
	}
	var rec string // an invocation (and its completion) or settle record is no step
	switch {
	case s.Invocation:
		rec = "|invoked"
	case s.Settled:
		rec = "|settled"
	case s.Call != 0:
		rec = fmt.Sprintf("|call%d", s.Call)
	}
	if s.Ambiguous {
		rec += "|assumed"
	}
	return fmt.Sprintf("%s|r%d|p%d|t%d|#%d|%s|%q|%s|%v|%v%s%s;", s.Actor, k.id("r", s.Recorder), k.id("p", s.Process), k.id("t", s.Thread),
		s.Seq, s.Outcome, s.Error, s.String(), s.Obs, s.Order, times, rec)
}

func (k *Keyer) id(kind, name string) int {
	if k.ids == nil {
		k.ids = map[string]int{}
	}
	key := kind + ":" + name
	n, ok := k.ids[key]
	if !ok {
		n = len(k.ids)
		k.ids[key] = n
	}
	return n
}

// Key is the verdict cache key of a run (Keyer).
func Key(steps []Step) string {
	var k Keyer
	b := make([]byte, 0, 64*len(steps))
	for i := range steps {
		b = append(b, k.Step(&steps[i])...)
	}
	return string(b)
}
