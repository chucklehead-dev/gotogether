package qoracle

import (
	"context"
	"encoding/json"
	"slices"

	"github.com/chucklehead-dev/gotogether/internal/qfront"
	"github.com/chucklehead-dev/gotogether/internal/qoracle/canon"
)

// Q2's Go front end in the harness, at P1 (docs/q2-front-end.md §5): its
// parse alone, compared with quint's in the P1 tier (ParseTier). The
// stub GOTOGETHER_QFRONT_STUB=go runs the corpus so.

// GoParse is internal/qfront's parse as a front end: `quint parse --out
// --source-map`'s documents, cut to the P1 tier.
type GoParse struct{}

// Name is "go-parse".
func (GoParse) Name() string { return "go-parse" }

// Run parses a staged copy of p.
func (GoParse) Run(_ context.Context, p Program, dir string) (*Output, error) {
	file, err := stage(p, dir)
	if err != nil {
		return nil, err
	}
	parsed, err := qfront.Parse(file)
	if err != nil {
		return nil, err
	}
	return P1(&Output{Root: dir, Parse: parsed.JSON(), SourceMap: parsed.SourceMapJSON()})
}

// ParseTier is a front end's output cut to the P1 tier (P1): the oracle's
// side of GoParse's comparison. Run it on Quint{ParseOnly: true}.
type ParseTier struct {
	FrontEnd
}

// Name is the front end's, in the P1 tier.
func (t ParseTier) Name() string { return t.FrontEnd.Name() + "-parse" }

// Run is the front end's output, cut.
func (t ParseTier) Run(ctx context.Context, p Program, dir string) (*Output, error) {
	out, err := t.FrontEnd.Run(ctx, p, dir)
	if err != nil {
		return nil, err
	}
	return P1(out)
}

// P1Codes are the codes of the errors P1 reports: syntax errors (QNT000
// from the parser), Quint.g4's own (QNT006, QNT008, QNT009), the
// listener's (QNT007, QNT012, QNT014, QNT015, QNT016) and source
// resolution's (QNT013, QNT408). Read-only.
var P1Codes = []string{"QNT000", "QNT006", "QNT007", "QNT008", "QNT009", "QNT012", "QNT013", "QNT014", "QNT015", "QNT016", "QNT408"}

// P1 is an output cut to what P1 builds (§5's exit criteria): the parse
// (no typecheck, no compile), without its lookup table (P2's), its errors
// those of P1Codes. Where a syntax error (QNT000) is among them, only the
// first error is kept, by its code and start (ANTLR goes on after an error
// with a recovery P1 does not reproduce; it stops), and neither the
// modules nor the source map.
func P1(o *Output) (*Output, error) {
	out := &Output{Root: o.Root, SourceMap: o.SourceMap}
	if o.Parse == nil {
		return out, nil
	}
	v, err := decodeJSON(o.Parse)
	if err != nil {
		return nil, err
	}
	top, _ := v.(map[string]any)
	delete(top, "table")
	es, _ := top["errors"].([]any)
	var kept []any
	syntaxErr := false
	for _, e := range es {
		em, _ := e.(map[string]any)
		expl, _ := em["explanation"].(string)
		code := canon.Code(expl)
		if !slices.Contains(P1Codes, code) {
			continue
		}
		syntaxErr = syntaxErr || code == "QNT000"
		kept = append(kept, e)
	}
	if syntaxErr {
		first, _ := kept[0].(map[string]any)
		if locs, _ := first["locs"].([]any); len(locs) > 0 {
			for _, l := range locs {
				if lm, ok := l.(map[string]any); ok {
					delete(lm, "end")
				}
			}
		}
		kept = kept[:1]
		top["modules"] = []any{}
		out.SourceMap = nil
	}
	if kept == nil {
		kept = []any{}
	}
	top["errors"] = kept
	if out.Parse, err = json.Marshal(top); err != nil {
		return nil, err
	}
	return out, nil
}
