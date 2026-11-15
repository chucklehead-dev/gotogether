package model

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/formats"
	"github.com/chucklehead-dev/gotogether/validate"
)

// A MatrixReport is a design's variants checked side by side (Matrix):
// each variant's own Report, and the table of what each must hold or be
// able to break, properties by variants (ROADMAP CW2).
type MatrixReport struct {
	// Binding is the binding's path as given.
	Binding string
	// Variants are each variant's name, constants and Report, in the
	// binding's order.
	Variants []VariantReport
	// Rows are the table's: one per invariant or eventual val that some
	// variant classifies, in the order the variants first name them.
	Rows []MatrixRow
	// Code is the worst of the variants' Codes (1 over 2): 0 when every
	// variant is as the binding says.
	Code int
}

// A VariantReport is one variant's run: its name, the constants it sets
// over the section's, and the report of the model under them.
type VariantReport struct {
	Name      string
	Constants map[string]string
	Report    *Report
}

// A MatrixRow is one property across the variants: Cells in the variants'
// order.
type MatrixRow struct {
	Check string
	Cells []MatrixCell
}

// A MatrixCell is one property in one variant. Expect is the variant's
// classification ("holds", "allows" for must break, "" for neither),
// Verdict what the run found ("holds", "bounded", "breaks", "unchecked",
// "" when the variant does not check it), Steps the counterexample's
// length when it breaks (0 for none known), Depth the depth a "holds" was
// proved to (0 for the whole state graph: a search cut only by its depth
// proves every trace that long, not every trace), and OK whether that is
// what the variant expects.
type MatrixCell struct {
	Variant string
	Expect  string
	Verdict string
	Steps   int
	Depth   int
	OK      bool
}

// Matrix checks each of the binding's simulate.variants (Binding.Variant)
// with opt, as Simulate would check the binding with that variant's
// constants and classifications, and tabulates them. A variant whose
// invariants are all ones it must break, with no eventual val, stops its
// graph once each has broken (GraphSearch.UntilBroken): its
// counterexamples are the shortest still, and the rest of its graph could
// only cost memory. A variant that
// cannot run (a constant the model lacks, an unknown invariant) is an
// error naming it: the matrix is the design's record, and a column left
// out of it would read as a pass.
func Matrix(ctx context.Context, bindingPath string, opt SimulateOptions) (*MatrixReport, error) {
	// a variant that must only break stops at its counterexamples: the
	// rest of its graph would show nothing more the matrix reports
	opt.UntilBroken = true
	b, err := binding.Load(bindingPath)
	if err != nil {
		return nil, err
	}
	if b.Simulate == nil || len(b.Simulate.Variants) == 0 {
		return nil, fmt.Errorf("simulate -matrix: %s's simulate section has no variants: list the design's alternatives under simulate.variants, each a name, its constants, and what it holds and allows", bindingPath)
	}
	m := &MatrixReport{Binding: bindingPath}
	for _, v := range b.Simulate.Variants {
		vb, err := b.Variant(v.Name)
		if err != nil {
			return nil, err
		}
		rep, err := simulate(ctx, bindingPath, vb, nil, opt)
		if err != nil {
			return nil, fmt.Errorf("simulate -matrix: variant %s: %w", v.Name, err)
		}
		m.Variants = append(m.Variants, VariantReport{Name: v.Name, Constants: maps.Clone(vb.Simulate.Constants), Report: rep})
		if rep.Code == 1 || m.Code == 0 {
			m.Code = rep.Code // a failure wins over no verdict, either over a pass
		}
	}
	m.Rows = matrixRows(b, m.Variants)
	return m, nil
}

// matrixRows are the table's rows: every property some variant
// classifies, each judged in every variant.
func matrixRows(b *binding.Binding, variants []VariantReport) []MatrixRow {
	var checks []string
	seen := map[string]bool{}
	for _, v := range variants {
		sec := v.Report.section
		for _, n := range append(slices.Clone(sec.Holds), sec.Allows...) {
			if !seen[n] {
				seen[n] = true
				checks = append(checks, n)
			}
		}
	}
	var rows []MatrixRow
	for _, n := range checks {
		row := MatrixRow{Check: n}
		for _, v := range variants {
			row.Cells = append(row.Cells, matrixCell(v, n))
		}
		rows = append(rows, row)
	}
	return rows
}

func matrixCell(v VariantReport, name string) MatrixCell {
	c := MatrixCell{Variant: v.Name}
	sec := v.Report.section
	switch {
	case slices.Contains(sec.Holds, name):
		c.Expect = "holds"
	case slices.Contains(sec.Allows, name):
		c.Expect = "allows"
	}
	var vd *Verdict
	for _, list := range [][]Verdict{v.Report.Invariants, v.Report.Eventually} {
		for i := range list {
			if list[i].Name == name {
				vd = &list[i]
			}
		}
	}
	switch {
	case vd == nil:
		return c
	case vd.Error != "" || vd.Unchecked != "":
		c.Verdict = "unchecked"
	case vd.Violated:
		c.Verdict = "breaks"
		switch {
		case vd.Counterexample != nil:
			c.Steps = len(vd.Counterexample.Steps) - 1
		case vd.Lasso != nil && vd.Lasso.Prefix != nil:
			c.Steps = len(vd.Lasso.Prefix.Steps) - 1 + len(vd.Lasso.Cycle)
		}
	case vd.Held && vd.Bound.Proves():
		c.Verdict = "holds"
		switch vd.Bound.Kind {
		case "depth":
			c.Depth = vd.Bound.Depth
		case "verified":
			c.Depth = vd.Bound.Steps
		}
	case vd.Held:
		c.Verdict = "bounded"
	}
	switch c.Expect {
	case "holds":
		c.OK = c.Verdict == "holds"
	case "allows":
		c.OK = c.Verdict == "breaks"
	default:
		c.OK = true // unclassified here: whatever it found is no failure
	}
	return c
}

// Text is the matrix as a Markdown table: a column per variant, a row per
// property, each cell what the run found, marked FAIL where the variant
// says otherwise. It is what `-write-matrix` puts in a design document.
func (m *MatrixReport) Text() string {
	var b strings.Builder
	b.WriteString("| |")
	for _, v := range m.Variants {
		fmt.Fprintf(&b, " %s |", v.Name)
	}
	b.WriteString("\n|---|")
	for range m.Variants {
		b.WriteString("---|")
	}
	b.WriteString("\n")
	for _, r := range m.Rows {
		fmt.Fprintf(&b, "| %s |", r.Check)
		for _, c := range r.Cells {
			fmt.Fprintf(&b, " %s |", c.Text())
		}
		b.WriteString("\n")
	}
	return b.String()
}

// Text is the cell as the table shows it: "holds" (the whole state graph),
// "holds to depth 8" (a bounded proof), "breaks in 3 steps", "FAIL: never
// breaks" and the like, "–" where the variant does not check the property.
func (c MatrixCell) Text() string {
	found := c.Verdict
	switch c.Verdict {
	case "":
		return "–"
	case "breaks":
		if c.Steps > 0 {
			found = fmt.Sprintf("breaks in %d step%s", c.Steps, map[bool]string{true: "", false: "s"}[c.Steps == 1])
		}
	case "holds":
		if c.Depth > 0 {
			found = fmt.Sprintf("holds to depth %d", c.Depth)
		}
	case "bounded":
		found = "holds within the bound"
	}
	if c.OK {
		return found
	}
	switch c.Expect {
	case "holds":
		return "FAIL: " + found + " (must hold)"
	default:
		return "FAIL: " + found + " (must break)"
	}
}

// Doc is the matrix as data (gotogether.matrix/1): each variant's report
// document and the table's rows.
func (m *MatrixReport) Doc() *validate.MatrixDocument {
	d := &validate.MatrixDocument{Format: formats.Matrix.Name(), Binding: m.Binding, Exit: m.Code, Table: m.Text(),
		Variants: []validate.MatrixVariant{}, Rows: []validate.MatrixRowDoc{}}
	switch m.Code {
	case 0:
		d.Verdict = "pass"
	case 1:
		d.Verdict = "fail"
	default:
		d.Verdict = "no verdict"
	}
	for _, v := range m.Variants {
		d.Variants = append(d.Variants, validate.MatrixVariant{Name: v.Name, Constants: v.Constants, Report: v.Report.Doc()})
	}
	for _, r := range m.Rows {
		row := validate.MatrixRowDoc{Check: r.Check}
		for _, c := range r.Cells {
			row.Cells = append(row.Cells, validate.MatrixCellDoc{Variant: c.Variant, Expect: c.Expect, Verdict: c.Verdict, Steps: c.Steps, Depth: c.Depth, OK: c.OK})
		}
		d.Rows = append(d.Rows, row)
	}
	return d
}
