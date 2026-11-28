package lint

import (
	"context"

	"github.com/chucklehead-dev/gotogether/internal/veilfrag"
)

// veil is the check at of the model file path for quint's Veil backend:
// its idioms Veil refuses, the assumes main ("": quint's default main)
// does not see, and the holds invariants a settle flag step never sets
// makes vacuous. Notes unless o.VeilErrors.
func (r *Report) veil(ctx context.Context, at int, o Options, path, main string, holds []string) {
	p, err := veilfrag.Parse(ctx, o.Quint, path)
	if err != nil {
		r.fail(at, "model.veil.parse", err, path, "make the model parse (quint parse), with quint installed (gotogether doctor)", "tool", true)
		return
	}
	if main == "" {
		main = p.Main()
	}
	severity := Info
	if o.VeilErrors {
		severity = Error
	}
	fs := p.Findings(main)
	fs = append(fs, p.Vacuous(main, holds)...)
	r.pass(at)
	for _, f := range fs {
		r.Findings = append(r.Findings, Diagnostic{Code: f.Code, Severity: severity, Message: f.Message, Path: f.Path, Line: f.Line, Column: f.Column, Symbol: f.Symbol, Fix: f.Fix, Confidence: "ir"})
	}
	if len(fs) > 0 && o.VeilErrors {
		r.Checks[at].Status = CheckFail
	}
}
