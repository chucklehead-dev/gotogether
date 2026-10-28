package validate

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/chucklehead-dev/gotogether/internal/qeval"
)

// A grist application's model (grist's generator writes its step
// actions over grist.qnt's library) rejects a step through a few
// well-known definitions. gristReading says what a false one means in
// the application's terms; it reads the frames of the explanation (the
// definitions applied, with their arguments), so a definition of another
// model's that shares a name and its parameters reads the same: a
// reading is a hint beside the conditions, never a verdict.
//
//   - covers(reads, need): the decision read nothing of the events need
//     selects; the read models whose reads cover them are named.
//   - conditionHolds(log, readPos, reads): an event appended after the
//     decision's read position matches its reads: a concurrent append
//     the commit should have conflicted on.
//   - freshKey(answered, id): the command id was answered already.
//   - <call>Admits(..., r): a Keyed dependency's served record applies
//     its key a second time.
func gristReading(e *evaluator, consts map[string]qeval.Value, s qeval.State, w *qeval.Why) string {
	for i := len(w.Chain) - 1; i >= 0; i-- {
		f := w.Chain[i].Frame
		if f == nil {
			continue
		}
		switch {
		case f.Name == "covers" && hasParams(f, "reads", "need"):
			return coversReading(e, consts, s, f)
		case f.Name == "conditionHolds" && hasParams(f, "log", "readPos", "reads"):
			return conditionReading(f)
		case f.Name == "freshKey" && hasParams(f, "answered", "id"):
			id, _ := f.Arg("id")
			return fmt.Sprintf("command id %s was answered already: a key's first answer is final, and this step answers it again", qeval.String(id))
		case strings.HasSuffix(f.Name, "Admits") && len(f.Name) > len("Admits"):
			r, ok := f.Arg("r")
			if !ok {
				continue
			}
			key := "?"
			if rec, ok := r.(*qeval.Rec); ok {
				if k, ok := field(rec, "key"); ok {
					key = qeval.String(k)
				}
			}
			dep := strings.TrimSuffix(f.Name, "Admits")
			return fmt.Sprintf("the dependency of %s applied key %s a second time: it is Keyed (each key applied once), so a second application of one key is no behaviour of it", kebab(dep), key)
		}
	}
	return ""
}

func hasParams(f *qeval.Frame, ps ...string) bool {
	for _, p := range ps {
		if _, ok := f.Arg(p); !ok {
			return false
		}
	}
	return true
}

func field(r *qeval.Rec, name string) (qeval.Value, bool) {
	for i, n := range r.Names {
		if n == name {
			return r.Vals[i], true
		}
	}
	return nil, false
}

// a Read of grist.qnt's: its event type and the tags it requires
type read struct {
	typ  string
	tags [][2]string
}

func readOf(v qeval.Value) (read, bool) {
	rec, ok := v.(*qeval.Rec)
	if !ok {
		return read{}, false
	}
	t, ok1 := field(rec, "typ")
	tags, ok2 := field(rec, "tags")
	typ, ok3 := t.(qeval.Str)
	set, ok4 := tags.(*qeval.Set)
	if !ok1 || !ok2 || !ok3 || !ok4 {
		return read{}, false
	}
	r := read{typ: string(typ)}
	for _, x := range set.Elems {
		tup, ok := x.(qeval.Tup)
		if !ok || len(tup) != 2 {
			return read{}, false
		}
		k, ok1 := tup[0].(qeval.Str)
		v, ok2 := tup[1].(qeval.Str)
		if !ok1 || !ok2 {
			return read{}, false
		}
		r.tags = append(r.tags, [2]string{string(k), string(v)})
	}
	return r, true
}

func (r read) String() string {
	if len(r.tags) == 0 {
		return r.typ + " events"
	}
	var ts []string
	for _, t := range r.tags {
		ts = append(ts, fmt.Sprintf("%s=%q", t[0], t[1]))
	}
	return r.typ + " events tagged " + strings.Join(ts, ", ")
}

// covers is grist.qnt's covers: some read selects every event need does.
func (r read) covers(need read) bool {
	if r.typ != need.typ {
		return false
	}
	for _, t := range r.tags {
		found := false
		for _, u := range need.tags {
			if t == u {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// coversReading names what the decision did not read, and the read
// models of the model (each <rm>Reads, a val or a def of its key) whose
// reads cover it.
func coversReading(e *evaluator, consts map[string]qeval.Value, s qeval.State, f *qeval.Frame) string {
	nv, _ := f.Arg("need")
	need, ok := readOf(nv)
	if !ok {
		return ""
	}
	rv, _ := f.Arg("reads")
	got := "nothing"
	if set, ok := rv.(*qeval.Set); ok && len(set.Elems) > 0 {
		var rs []string
		for _, x := range set.Elems {
			if r, ok := readOf(x); ok {
				rs = append(rs, r.String())
			}
		}
		got = strings.Join(rs, "; ")
	}
	var models []string
	for _, d := range e.m.DefsEndingIn("Reads") {
		var args []qeval.Value
		switch len(d.Params) {
		case 0:
		case 1:
			// a keyed read model's key is the value of a tag of the events
			// it folds: each of need's tags' values, tried
			for _, t := range need.tags {
				args = append(args, qeval.Str(t[1]))
			}
		default:
			continue
		}
		tries := [][]qeval.Value{nil}
		if len(d.Params) == 1 {
			tries = nil
			for _, a := range args {
				tries = append(tries, []qeval.Value{a})
			}
		}
		for _, as := range tries {
			if readModelCovers(e, consts, s, d.Name, as, need) {
				models = append(models, kebab(strings.TrimSuffix(d.Display, "Reads")))
				break
			}
		}
	}
	through := "through the Reads its rule depends on (a read of the event type with those tags)"
	if len(models) > 0 {
		through = "through the read model " + strings.Join(models, " or ") + ", or the Reads of the event type"
	}
	return fmt.Sprintf("the decision read nothing of %s (its read set: %s), which its rule needs: read it %s, so a concurrent change conflicts", need, got, through)
}

// readModelCovers reports whether read model def's reads (applied to
// args) cover need.
func readModelCovers(e *evaluator, consts map[string]qeval.Value, s qeval.State, def string, args []qeval.Value, need read) bool {
	v, err := e.m.Eval(consts, s, def, args...)
	set, ok := v.(*qeval.Set)
	if err != nil || !ok {
		return false
	}
	for _, x := range set.Elems {
		if r, ok := readOf(x); ok && r.covers(need) {
			return true
		}
	}
	return false
}

// conditionReading names the event appended after the decision's read
// position that matches its reads.
func conditionReading(f *qeval.Frame) string {
	lv, _ := f.Arg("log")
	pv, _ := f.Arg("readPos")
	rv, _ := f.Arg("reads")
	log, ok1 := lv.(qeval.List)
	pos, ok2 := pv.(qeval.Int)
	reads, ok3 := rv.(*qeval.Set)
	if !ok1 || !ok2 || !ok3 {
		return ""
	}
	if int(pos) > len(log) || pos < 0 {
		return fmt.Sprintf("the decision read through position %d of a log of %d events: past its head", pos, len(log))
	}
	for i := int(pos); i < len(log); i++ {
		ev, ok := log[i].(*qeval.Rec)
		if !ok {
			continue
		}
		t, _ := field(ev, "typ")
		tags, _ := field(ev, "tags")
		typ, _ := t.(qeval.Str)
		got := read{typ: string(typ)}
		if set, ok := tags.(*qeval.Set); ok {
			if r, ok := readOf(qeval.NewRec([]string{"typ", "tags"}, []qeval.Value{t, set})); ok {
				got = r
			}
		}
		for _, x := range reads.Elems {
			if r, ok := readOf(x); ok && r.covers(got) {
				return fmt.Sprintf("event %d (%s), appended after the decision read through position %d, matches its read of %s: a concurrent append, which the commit should have conflicted on (its condition)", i+1, got, pos, r)
			}
		}
	}
	return ""
}

// kebab is a Quint name as grist's Go names are written: counterNames
// is counter-names.
func kebab(s string) string {
	var b strings.Builder
	for i, r := range s {
		if unicode.IsUpper(r) {
			if i > 0 {
				b.WriteByte('-')
			}
			r = unicode.ToLower(r)
		}
		b.WriteRune(r)
	}
	return b.String()
}
