package qoracle

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/chucklehead-dev/gotogether/internal/qoracle/canon"
)

// Tier is the stage at which two front ends disagree (§4.3's tiers 1 to
// 4; tier 5, meaning, is qeval's and waits for P2 to P4).
type Tier string

const (
	// TierParse: the parsed modules, table or source-map spans.
	TierParse Tier = "parse"
	// TierTypecheck: the typechecked modules or types.
	TierTypecheck Tier = "typecheck"
	// TierCompile: the compile IR.
	TierCompile Tier = "compile"
	// TierErrors: the first error's code, file, start or end (First), or
	// only the rest of the errors.
	TierErrors Tier = "errors"
)

// ToolFailure is the verdict of a compile that wrote nothing (CompileFailed).
const ToolFailure = "tool-failure"

// A Disagreement is where two front ends' outputs differ, in their
// canonical forms (canon, tier Read).
type Disagreement struct {
	Tier Tier
	// Kind is the output that differs (parse, typecheck, compile).
	Kind canon.Kind
	// First: the first error differs (code, file, start or end), not only
	// a later one.
	First bool
	// Diffs are the differences, in path order.
	Diffs []canon.Difference
}

// Path is the first difference's path: what a shrink keeps.
func (d *Disagreement) Path() string {
	if d == nil || len(d.Diffs) == 0 {
		return ""
	}
	return d.Diffs[0].Path
}

func (d *Disagreement) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s (%s output", d.Tier, d.Kind)
	if d.Tier == TierErrors {
		if d.First {
			b.WriteString(", the first error")
		} else {
			b.WriteString(", a later error")
		}
	}
	fmt.Fprintf(&b, "), %d difference(s)", len(d.Diffs))
	for i, x := range d.Diffs {
		if i == 5 {
			fmt.Fprintf(&b, "\n  ... %d more", len(d.Diffs)-5)
			break
		}
		b.WriteString("\n  " + x.String())
	}
	return b.String()
}

// Compare runs a and b on p and compares their outputs: each by its
// canonical form, the first output that differs (parse, then typecheck,
// then compile) its disagreement; nil when they agree. tmp holds the
// runs' copies. Every field is compared: Q2's front end must match
// quint's own order (CompareKnown's classes included).
func Compare(ctx context.Context, p Program, a, b FrontEnd, tmp string) (*Disagreement, error) {
	oa, ob, err := RunBoth(ctx, p, a, b, tmp)
	if err != nil {
		return nil, err
	}
	return CompareOutputs(oa, ob)
}

// A Class is a way quint 0.33.1's output changes when its own ids shift
// (q2-front-end.md §4.5's rule). Where it does, there is no quint
// behaviour to match: gotogether's front end makes an id-free choice
// (source order, then name), and the oracle accepts the class, against
// any front end, wherever testdata/shift-known.txt lists it. Where quint's
// output does not change under the shift, it is matched exactly.
type Class string

const (
	// ClassCycleOrder: QNT099's errors, one per declaration of a cycle, in
	// the order of an immutable Map hashed by the declarations' ids
	// (toposort.ts's `cycles: Set(edges.keys())`), and QNT098's listing
	// of a cycle's modules in the same Map's order, its reference the
	// first. Accepted: the errors the same set, a cycle's declarations as
	// a set of (code, place) and its modules' listing as a set; only which
	// QNT099 comes first, and where a QNT098 points, may differ.
	ClassCycleOrder Class = "cycle-order"
	// ClassTypesAfterError: after a module's first type error quint types
	// nothing more (constraintGenerator.ts: each exit returns once its
	// errors are not empty), and it walks a layer's declarations in the
	// ids' order. Accepted, in the typecheck's output and a failed
	// compile's: the same errors, the same type for every node both type,
	// other nodes typed.
	ClassTypesAfterError Class = "types-after-error"
	// ClassLayerOrder: a toposort layer sorted by its ids as strings. The
	// form compares declarations and modules by layer, so it never shows
	// as a difference; listed by name with the others.
	ClassLayerOrder Class = "layer-order"
	// ClassVerdictFlip: a corpus entry quint accepts unshifted and refuses
	// shifted, or the reverse: an upstream bug (docs/upstream/), whose
	// unshifted verdict Q2 implements. Listed per entry, never for every
	// program.
	ClassVerdictFlip Class = "verdict-flip"
)

// classes are the classes the oracle knows (classOf tells the first two;
// layer-order is the form's, verdict-flip the corpus test's). Read-only.
var classes = map[Class]bool{ClassCycleOrder: true, ClassTypesAfterError: true, ClassLayerOrder: true, ClassVerdictFlip: true}

// shiftKnownText is testdata/shift-known.txt. Read-only.
//
//go:embed testdata/shift-known.txt
var shiftKnownText string

// A ShiftKnown line: a class accepted where scope says (* every program,
// else a corpus entry's id), and what quint does.
type ShiftKnown struct {
	Class         Class
	Scope, Reason string
}

// ShiftKnownLines is testdata/shift-known.txt read: each line a class, a
// scope, " # " and the reason.
func ShiftKnownLines() ([]ShiftKnown, error) { return parseShiftKnown(shiftKnownText) }

func parseShiftKnown(text string) ([]ShiftKnown, error) {
	var out []ShiftKnown
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		head, reason, ok := strings.Cut(l, " # ")
		f := strings.Fields(head)
		if !ok || len(f) != 2 {
			return nil, fmt.Errorf("testdata/shift-known.txt: %q: want a class, a scope (* or an entry's id), \" # \" and the reason", l)
		}
		c := Class(f[0])
		if !classes[c] {
			return nil, fmt.Errorf("testdata/shift-known.txt: %q: no class %s (the oracle's are cycle-order, types-after-error, layer-order, verdict-flip)", l, c)
		}
		if (c == ClassVerdictFlip) == (f[1] == "*") {
			return nil, fmt.Errorf("testdata/shift-known.txt: %q: verdict-flip names one entry, every other class *", l)
		}
		out = append(out, ShiftKnown{c, f[1], reason})
	}
	return out, nil
}

// accepted are the classes testdata/shift-known.txt accepts for every
// program. Read-only after init.
var accepted = func() map[Class]bool {
	ls, err := ShiftKnownLines()
	if err != nil {
		panic(err)
	}
	out := map[Class]bool{}
	for _, l := range ls {
		if l.Scope == "*" {
			out[l.Class] = true
		}
	}
	return out
}()

// A Known is an output's difference that is an accepted class.
type Known struct {
	Class Class
	Kind  canon.Kind
	Diffs []canon.Difference
}

func (k Known) String() string {
	return fmt.Sprintf("%s (%s output, %d difference(s))", k.Class, k.Kind, len(k.Diffs))
}

// CompareKnown is Compare with testdata/shift-known.txt's classes
// accepted: an output whose only difference is one of them is listed, not
// a disagreement, and the outputs after it are compared. It is the
// oracle's comparison, of the shifted stub and of a Go front end alike;
// Compare is the form's alone.
func CompareKnown(ctx context.Context, p Program, a, b FrontEnd, tmp string) (*Disagreement, []Known, error) {
	oa, ob, err := RunBoth(ctx, p, a, b, tmp)
	if err != nil {
		return nil, nil, err
	}
	return CompareOutputsKnown(oa, ob)
}

// CompareOutputsKnown compares two outputs as CompareKnown does.
func CompareOutputsKnown(oa, ob *Output) (*Disagreement, []Known, error) {
	return compareOutputs(oa, ob, accepted)
}

// RunBoth is a's and b's outputs for p.
func RunBoth(ctx context.Context, p Program, a, b FrontEnd, tmp string) (*Output, *Output, error) {
	oa, err := runIn(ctx, a, p, tmp)
	if err != nil {
		return nil, nil, err
	}
	ob, err := runIn(ctx, b, p, tmp)
	if err != nil {
		return nil, nil, err
	}
	return oa, ob, nil
}

// VerdictOf is the verdict an output says: its typecheck's and compile's
// error codes.
func VerdictOf(o *Output) (Verdict, error) {
	errs, err := Errors(o.Typecheck, canon.Typecheck, o.Root)
	if err != nil {
		return Verdict{}, err
	}
	v := Verdict{Typecheck: orNone(Codes(errs))}
	if o.CompileFailed != "" {
		v.Compile = []string{ToolFailure}
	}
	if o.Compile != nil {
		cerrs, err := Errors(o.Compile, canon.Compile, o.Root)
		if err != nil {
			return Verdict{}, err
		}
		v.Compile = orNone(Codes(cerrs))
	}
	return v, nil
}

func runIn(ctx context.Context, fe FrontEnd, p Program, tmp string) (*Output, error) {
	dir, err := os.MkdirTemp(tmp, fe.Name()+"-")
	if err != nil {
		return nil, err
	}
	out, err := fe.Run(ctx, p, dir)
	if err != nil {
		return nil, fmt.Errorf("%s on %s: %w", fe.Name(), p.ID, err)
	}
	return out, nil
}

// CompareOutputs compares two outputs by canonical form (Compare's).
func CompareOutputs(oa, ob *Output) (*Disagreement, error) {
	d, _, err := compareOutputs(oa, ob, nil)
	return d, err
}

func compareOutputs(oa, ob *Output, accept map[Class]bool) (*Disagreement, []Known, error) {
	var known []Known
	type doc struct {
		kind       canon.Kind
		tier       Tier
		a, b       []byte
		smA, smB   []byte
		hasA, hasB bool
	}
	docs := []doc{
		{canon.Parse, TierParse, oa.Parse, ob.Parse, oa.SourceMap, ob.SourceMap, oa.Parse != nil, ob.Parse != nil},
		{canon.Typecheck, TierTypecheck, oa.Typecheck, ob.Typecheck, nil, nil, oa.Typecheck != nil, ob.Typecheck != nil},
		{canon.Compile, TierCompile, oa.Compile, ob.Compile, nil, nil, oa.Compile != nil, ob.Compile != nil},
	}
	if (oa.CompileFailed != "") != (ob.CompileFailed != "") {
		return &Disagreement{Tier: TierCompile, Kind: canon.Compile, Diffs: []canon.Difference{{Path: "(compile failed)", A: oa.CompileFailed, B: ob.CompileFailed}}}, nil, nil
	}
	for _, d := range docs {
		if d.hasA != d.hasB {
			return &Disagreement{Tier: d.tier, Kind: d.kind, Diffs: []canon.Difference{{Path: "(output)", A: present(d.hasA), B: present(d.hasB)}}}, known, nil
		}
		if !d.hasA {
			continue
		}
		fa, err := canon.Of(d.a, d.smA, canon.Options{Kind: d.kind, Tier: canon.Read, Roots: []string{oa.Root}, Places: oa.SourceMap})
		if err != nil {
			return nil, nil, err
		}
		fb, err := canon.Of(d.b, d.smB, canon.Options{Kind: d.kind, Tier: canon.Read, Roots: []string{ob.Root}, Places: ob.SourceMap})
		if err != nil {
			return nil, nil, err
		}
		diffs := canon.Diff(fa, fb)
		if len(diffs) == 0 {
			continue
		}
		if c, ok := classOf(d.kind, fa, fb, diffs, oa, ob); ok && accept[c] {
			known = append(known, Known{Class: c, Kind: d.kind, Diffs: diffs})
			continue
		}
		dis := &Disagreement{Tier: d.tier, Kind: d.kind, Diffs: diffs}
		if onlyErrors(diffs) {
			// errors alone: the first, or a later one
			dis.Tier = TierErrors
			dis.First = firstDiffers(fa, fb)
		}
		return dis, known, nil
	}
	return nil, known, nil
}

func onlyErrors(diffs []canon.Difference) bool {
	for _, x := range diffs {
		if !strings.HasPrefix(x.Path, "errors") {
			return false
		}
	}
	return true
}

// classOf is the class two forms' differences are, if they are one.
// Cycle order: only the errors differ, the two outputs' errors are the
// same set once each QNT098 is its cycle's modules as a set (its place
// dropped: it points at the cycle's first), and their first errors are
// the same or both a QNT099, so the outputs differ only in which of a
// cycle's declarations is reported first or where a QNT098 points. A
// missing or extra error, one at another place, another module in a
// cycle, or a first error of another code is a disagreement. Types after
// an error: a typecheck's forms (or a failed compile's, which writes the
// typecheck's modules and types with its errors) equal but in their
// types, both with errors, and no node typed by both typed otherwise.
func classOf(kind canon.Kind, fa, fb *canon.Form, diffs []canon.Difference, oa, ob *Output) (Class, bool) {
	if kind != canon.Parse && typesAfterError(fa, fb) {
		return ClassTypesAfterError, true
	}
	if !onlyErrors(diffs) {
		return "", false
	}
	doc := func(o *Output) []byte {
		switch kind {
		case canon.Parse:
			return o.Parse
		case canon.Typecheck:
			return o.Typecheck
		}
		return o.Compile
	}
	ea, err := cycleErrors(doc(oa), kind, oa.Root)
	if err != nil {
		return "", false
	}
	eb, err := cycleErrors(doc(ob), kind, ob.Root)
	if err != nil || len(ea) == 0 || len(eb) == 0 {
		return "", false
	}
	set := func(es []string) []string { return slices.Compact(slices.Sorted(slices.Values(es))) }
	if !slices.Equal(set(ea), set(eb)) {
		return "", false
	}
	if ea[0] != eb[0] && !(strings.HasPrefix(ea[0], "QNT099 ") && strings.HasPrefix(eb[0], "QNT099 ")) {
		return "", false
	}
	return ClassCycleOrder, true
}

// cycleErrors is a document's errors in quint's order, each its code and
// places, a QNT098 its code and its cycle's modules, sorted.
func cycleErrors(doc []byte, kind canon.Kind, root string) ([]string, error) {
	f, err := canon.Of(doc, nil, canon.Options{Kind: kind, Tier: canon.All, Roots: []string{root}})
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range formErrors(f) {
		m, _ := e.(map[string]any)
		code, _ := m["code"].(string)
		if code == "QNT098" {
			expl, _ := m["explanation"].(string)
			_, list, _ := strings.Cut(expl, "among: ")
			mods := strings.Split(list, ",")
			for i := range mods {
				mods[i] = strings.TrimSpace(mods[i])
			}
			slices.Sort(mods)
			out = append(out, code+" "+strings.Join(mods, ","))
			continue
		}
		// what the form compares of an error: its places and reference
		out = append(out, code+" "+string((&canon.Form{Tree: map[string]any{"locs": m["locs"], "reference": m["reference"]}}).JSON()))
	}
	return out, nil
}

// typesAfterError says whether two typecheck forms with errors differ
// only in which nodes have types (ClassTypesAfterError).
func typesAfterError(fa, fb *canon.Form) bool {
	if len(formErrors(fa)) == 0 || len(formErrors(fb)) == 0 {
		return false
	}
	ta, _ := fa.Tree.(map[string]any)
	tb, _ := fb.Tree.(map[string]any)
	if ta == nil || tb == nil {
		return false
	}
	without := func(top map[string]any) *canon.Form {
		out := map[string]any{}
		for k, v := range top {
			if k != "types" {
				out[k] = v
			}
		}
		return &canon.Form{Tree: out}
	}
	if !canon.Equal(without(ta), without(tb)) {
		return false
	}
	ya, _ := ta["types"].(map[string]any)
	yb, _ := tb["types"].(map[string]any)
	for k, va := range ya {
		if vb, ok := yb[k]; ok && !canon.Equal(&canon.Form{Tree: va}, &canon.Form{Tree: vb}) {
			return false
		}
	}
	return true
}

func formErrors(f *canon.Form) []any {
	top, _ := f.Tree.(map[string]any)
	es, _ := top["errors"].([]any)
	return es
}

func present(b bool) string {
	if b {
		return "written"
	}
	return ""
}

// firstDiffers says whether two forms' first errors differ.
func firstDiffers(a, b *canon.Form) bool {
	first := func(f *canon.Form) any {
		top, _ := f.Tree.(map[string]any)
		es, _ := top["errors"].([]any)
		if len(es) == 0 {
			return nil
		}
		return es[0]
	}
	return !canon.Equal(&canon.Form{Tree: first(a)}, &canon.Form{Tree: first(b)})
}

// An Error of a front end: its code and where (`file:line:col-line:col`,
// 0-based as quint's JSON writes them).
type Error struct {
	Code, At string
}

// Errors is a front end's errors in an output, as canon reads them.
func Errors(doc []byte, kind canon.Kind, root string) ([]Error, error) {
	f, err := canon.Of(doc, nil, canon.Options{Kind: kind, Tier: canon.Read, Roots: []string{root}})
	if err != nil {
		return nil, err
	}
	top, _ := f.Tree.(map[string]any)
	es, _ := top["errors"].([]any)
	var out []Error
	for _, e := range es {
		em, _ := e.(map[string]any)
		code, _ := em["code"].(string)
		at := ""
		if locs, _ := em["locs"].([]any); len(locs) > 0 {
			l, _ := locs[0].(map[string]any)
			pos := func(v any) string {
				m, _ := v.(map[string]any)
				return fmt.Sprintf("%v:%v", m["line"], m["col"])
			}
			at = fmt.Sprintf("%v:%s-%s", l["source"], pos(l["start"]), pos(l["end"]))
		}
		out = append(out, Error{code, at})
	}
	return out, nil
}

// Codes is the distinct codes of errs, in order.
func Codes(errs []Error) []string {
	var out []string
	seen := map[string]bool{}
	for _, e := range errs {
		if !seen[e.Code] {
			seen[e.Code] = true
			out = append(out, e.Code)
		}
	}
	return out
}
