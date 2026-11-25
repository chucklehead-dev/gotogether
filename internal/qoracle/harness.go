package qoracle

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
// runs' copies.
func Compare(ctx context.Context, p Program, a, b FrontEnd, tmp string) (*Disagreement, error) {
	oa, err := runIn(ctx, a, p, tmp)
	if err != nil {
		return nil, err
	}
	ob, err := runIn(ctx, b, p, tmp)
	if err != nil {
		return nil, err
	}
	return compareOutputs(oa, ob)
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

func compareOutputs(oa, ob *Output) (*Disagreement, error) {
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
	for _, d := range docs {
		if d.hasA != d.hasB {
			return &Disagreement{Tier: d.tier, Kind: d.kind, Diffs: []canon.Difference{{Path: "(output)", A: present(d.hasA), B: present(d.hasB)}}}, nil
		}
		if !d.hasA {
			continue
		}
		fa, err := canon.Of(d.a, d.smA, canon.Options{Kind: d.kind, Tier: canon.Read, Roots: []string{oa.Root}})
		if err != nil {
			return nil, err
		}
		fb, err := canon.Of(d.b, d.smB, canon.Options{Kind: d.kind, Tier: canon.Read, Roots: []string{ob.Root}})
		if err != nil {
			return nil, err
		}
		diffs := canon.Diff(fa, fb)
		if len(diffs) == 0 {
			continue
		}
		dis := &Disagreement{Tier: d.tier, Kind: d.kind, Diffs: diffs}
		var errs, rest []canon.Difference
		for _, x := range diffs {
			if strings.HasPrefix(x.Path, "errors") {
				errs = append(errs, x)
			} else {
				rest = append(rest, x)
			}
		}
		if len(rest) == 0 {
			// errors alone: the first, or a later one
			dis.Tier = TierErrors
			dis.First = firstDiffers(fa, fb)
		}
		return dis, nil
	}
	return nil, nil
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

// absFile is path made absolute.
func absFile(path string) string {
	if p, err := filepath.Abs(path); err == nil {
		return p
	}
	return path
}
