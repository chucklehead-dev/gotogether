package qtrace

import (
	"crypto/sha256"
	"encoding/hex"
	"path"
	"slices"
	"time"
)

// A Filter selects and sanitizes recorded steps before they are kept or
// validated: production logs carry customers' identifiers and more steps
// than a check needs. Selection keeps whole actors' steps in a window;
// sanitizing replaces identifiers with pseudonyms that keep equality (so
// a binding's interning and the model's comparisons mean what they did)
// and drops what the binding does not read.
type Filter struct {
	// Actors and Actions, if set, keep the steps whose actor or action
	// matches one of the patterns (path.Match: "checkout-*").
	Actors, Actions []string
	// Since and Until, if set, keep the steps recorded in [Since, Until).
	// A watermark past Until is kept as complete through just before it.
	Since, Until time.Time
	// Pseudonymize names the fields (arguments, "obs.<name>", "actor")
	// whose string values become pseudonyms: a salted hash, the same for
	// the same value. Salt keeps them from being reversed by guessing.
	Pseudonymize []string
	Salt         string
	// Keep, if set, lists by action the fields to keep (Binding.UsedFields:
	// the ones the binding reads); every other argument and observation,
	// and the error text, is dropped. Steps of actions it does not list
	// keep only their identity.
	Keep map[string][]string
	// KeepRefs keeps steps' refs (Step.Ref) when the filter sanitizes
	// (Pseudonymize or Keep set), which drops them otherwise: a ref is
	// free text, often a path or a URL with a user's name or a
	// customer's id in it, which no pseudonym reaches. A filter that only
	// selects keeps them.
	KeepRefs bool
}

func match(patterns []string, s string) bool {
	if len(patterns) == 0 {
		return true
	}
	for _, p := range patterns {
		if ok, _ := path.Match(p, s); ok {
			return true
		}
	}
	return false
}

// Pseudonym is the filter's pseudonym for v.
func (f Filter) Pseudonym(v string) string {
	h := sha256.Sum256([]byte(f.Salt + "\x00" + v))
	return "p" + hex.EncodeToString(h[:6])
}

// Apply returns the selected steps, sanitized; steps are not modified.
func (f Filter) Apply(steps []Step) []Step {
	pseud := map[string]bool{}
	for _, k := range f.Pseudonymize {
		pseud[k] = true
	}
	var out []Step
	kept := map[string]bool{} // recorders with a step kept: their loss records go with them
	for _, s := range steps {
		if s.Lost != "" {
			out = append(out, s) // a loss record is its recorder's, whatever of its steps were kept: what they cannot show
			continue
		}
		if !s.CompleteThrough.IsZero() {
			// a watermark too, but no later than the window's end: the
			// steps after Until are taken out, and a watermark past it
			// would say their absence is the system's
			if !f.Until.IsZero() && !s.CompleteThrough.Before(f.Until) {
				s.CompleteThrough = f.Until.Add(-1)
			}
			out = append(out, s)
			continue
		}
		if !match(f.Actors, s.Actor) || !match(f.Actions, s.Action) ||
			(!f.Since.IsZero() && s.Time.Before(f.Since)) || (!f.Until.IsZero() && !s.Time.Before(f.Until)) {
			continue
		}
		var keep map[string]bool
		if f.Keep != nil {
			keep = map[string]bool{}
			for _, k := range f.Keep[s.Action] {
				keep[k] = true
			}
			s.Error, s.Source = "", ""
		}
		if (f.Keep != nil || len(f.Pseudonymize) > 0) && !f.KeepRefs {
			s.Ref = ""
		}
		fields := func(m map[string]any, prefix string) map[string]any {
			if m == nil {
				return nil
			}
			c := map[string]any{}
			for k, v := range m {
				name := prefix + k
				if keep != nil && !keep[name] {
					continue
				}
				if str, ok := v.(string); ok && pseud[name] {
					v = f.Pseudonym(str)
				}
				c[k] = v
			}
			return c
		}
		s.Args, s.Obs = fields(s.Args, ""), fields(s.Obs, "obs.")
		if pseud["actor"] && s.Actor != "" {
			s.Actor = f.Pseudonym(s.Actor)
		}
		kept[s.Recorder] = true
		out = append(out, s)
	}
	return slices.DeleteFunc(out, func(s Step) bool {
		return (s.Lost != "" || !s.CompleteThrough.IsZero()) && !kept[s.Recorder] && s.Recorder != AllRecorders
	})
}
