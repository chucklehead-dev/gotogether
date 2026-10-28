package validate

import (
	"fmt"
	"slices"
	"strings"

	"github.com/chucklehead-dev/gotogether/internal/qeval"
)

// A failing step's report shows what the step changed in the model's
// state, or what the model expected it to change (I20), not the whole
// state: a dump of a ledger's map cut at 240 characters was cut mid-value,
// and the one field that differs was past the cut. The diff is by path,
// in the model's own names (the variables', the records' fields, the
// maps' keys), so it reads as the user's data: charges["ch_3"].refunded.

// A Change is one difference between two of the model's states: the
// value at Path (a variable, then .field, [key] or [index]) changed from
// Before to After, or (Kind "added", "removed") an element or a map's key
// appeared or went. Values are Quint literals.
type Change struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"` // "changed", "added" or "removed"
	Before string `json:"before,omitempty"`
	After  string `json:"after,omitempty"`
}

// String is the change as a report's line: charges["ch_3"].refunded: 700 -> 1200.
func (c Change) String() string {
	switch c.Kind {
	case "added":
		return c.Path + ": added " + clip(c.After, 160)
	case "removed":
		return c.Path + ": removed " + clip(c.Before, 160)
	}
	return c.Path + ": " + clip(c.Before, 160) + " -> " + clip(c.After, 160)
}

// clip is s cut to n bytes at most, saying so.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + " ..."
}

// changesShown is how many of a step's changes a report lists.
const changesShown = 12

// stateDiff is what changed from state a to state b, variable by
// variable in name order (mbt:: annotations aside), each variable named
// by display (the model's own name of an evaluation module's).
func stateDiff(a, b qeval.State, display func(string) string) []Change {
	names := make([]string, 0, len(b))
	for k := range b {
		if !strings.HasPrefix(k, "mbt::") {
			names = append(names, k)
		}
	}
	for k := range a {
		if _, ok := b[k]; !ok && !strings.HasPrefix(k, "mbt::") {
			names = append(names, k)
		}
	}
	slices.SortFunc(names, func(x, y string) int { return strings.Compare(display(x), display(y)) })
	var out []Change
	for _, n := range names {
		x, inA := a[n]
		y, inB := b[n]
		switch {
		case !inA:
			out = append(out, Change{Path: display(n), Kind: "added", After: qeval.String(y)})
		case !inB:
			out = append(out, Change{Path: display(n), Kind: "removed", Before: qeval.String(x)})
		default:
			diffValue(display(n), x, y, &out)
		}
	}
	return out
}

// diffValue adds to out what differs between x and y at path: into a
// record's fields, a map's keys, a set's elements, a list's or tuple's
// positions (of equal lengths) and a variant's payload (of one label);
// any other difference is the whole value's.
func diffValue(path string, x, y qeval.Value, out *[]Change) {
	if qeval.Compare(x, y) == 0 {
		return
	}
	whole := func() {
		*out = append(*out, Change{Path: path, Kind: "changed", Before: qeval.String(x), After: qeval.String(y)})
	}
	switch a := x.(type) {
	case *qeval.Rec:
		b, ok := y.(*qeval.Rec)
		if !ok || !slices.Equal(a.Names, b.Names) {
			whole()
			return
		}
		for i, n := range a.Names {
			diffValue(path+"."+n, a.Vals[i], b.Vals[i], out)
		}
	case *qeval.Map:
		b, ok := y.(*qeval.Map)
		if !ok {
			whole()
			return
		}
		i, j := 0, 0
		for i < len(a.Keys) || j < len(b.Keys) {
			c := 0
			switch {
			case i == len(a.Keys):
				c = 1
			case j == len(b.Keys):
				c = -1
			default:
				c = qeval.Compare(a.Keys[i], b.Keys[j])
			}
			switch {
			case c < 0:
				*out = append(*out, Change{Path: path + "[" + qeval.String(a.Keys[i]) + "]", Kind: "removed", Before: qeval.String(a.Vals[i])})
				i++
			case c > 0:
				*out = append(*out, Change{Path: path + "[" + qeval.String(b.Keys[j]) + "]", Kind: "added", After: qeval.String(b.Vals[j])})
				j++
			default:
				diffValue(path+"["+qeval.String(a.Keys[i])+"]", a.Vals[i], b.Vals[j], out)
				i++
				j++
			}
		}
	case *qeval.Set:
		b, ok := y.(*qeval.Set)
		if !ok {
			whole()
			return
		}
		i, j := 0, 0
		for i < len(a.Elems) || j < len(b.Elems) {
			c := 0
			switch {
			case i == len(a.Elems):
				c = 1
			case j == len(b.Elems):
				c = -1
			default:
				c = qeval.Compare(a.Elems[i], b.Elems[j])
			}
			switch {
			case c < 0:
				*out = append(*out, Change{Path: path, Kind: "removed", Before: qeval.String(a.Elems[i])})
				i++
			case c > 0:
				*out = append(*out, Change{Path: path, Kind: "added", After: qeval.String(b.Elems[j])})
				j++
			default:
				i++
				j++
			}
		}
	case qeval.List:
		b, ok := y.(qeval.List)
		if !ok || len(a) != len(b) {
			whole()
			return
		}
		for i := range a {
			diffValue(fmt.Sprintf("%s[%d]", path, i), a[i], b[i], out)
		}
	case qeval.Tup:
		b, ok := y.(qeval.Tup)
		if !ok || len(a) != len(b) {
			whole()
			return
		}
		for i := range a {
			diffValue(fmt.Sprintf("%s._%d", path, i+1), a[i], b[i], out)
		}
	case qeval.Variant:
		b, ok := y.(qeval.Variant)
		if !ok || a.Label != b.Label {
			whole()
			return
		}
		diffValue(path+"."+a.Label, a.Val, b.Val, out)
	default:
		whole()
	}
}

// writeChanges writes cs as a report's lines under head, each prefixed
// with pfx: at most changesShown of them unless verbose.
func writeChanges(b *strings.Builder, pfx, head string, cs []Change, verbose bool) {
	fmt.Fprintf(b, "%s%s\n", pfx, head)
	for i, c := range cs {
		if i == changesShown && !verbose {
			fmt.Fprintf(b, "%s  ... and %d more changes (-v lists them)\n", pfx, len(cs)-changesShown)
			break
		}
		fmt.Fprintf(b, "%s  %s\n", pfx, c)
	}
}
