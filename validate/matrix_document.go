package validate

// A MatrixDocument is a design matrix as data (gotogether.matrix/1,
// `gotogether simulate -matrix -json`): each variant's own report/2
// Document, and the table's rows, so a script reads what each variant
// holds and breaks without parsing the table's text.
type MatrixDocument struct {
	Format  string `json:"format"`
	Binding string `json:"binding"`
	// Verdict and Exit are the matrix's: "pass" (every variant as its
	// classifications say), "fail", or "no verdict", and the exit code.
	Verdict  string          `json:"verdict"`
	Exit     int             `json:"exit"`
	Variants []MatrixVariant `json:"variants"`
	Rows     []MatrixRowDoc  `json:"rows"`
	Table    string          `json:"table"`
}

// A MatrixVariant is one variant: its name, the constants it ran under
// (the section's with the variant's over them) and its report.
type MatrixVariant struct {
	Name      string            `json:"name"`
	Constants map[string]string `json:"constants,omitempty"`
	Report    *Document         `json:"report"`
}

// A MatrixRowDoc is one property across the variants, a cell per variant
// in their order.
type MatrixRowDoc struct {
	Check string          `json:"check"`
	Cells []MatrixCellDoc `json:"cells"`
}

// A MatrixCellDoc is one property in one variant: what the variant
// expects ("holds"; "allows", must break; "" for neither), what was found
// ("holds", "bounded", "breaks", "unchecked"; "" when the variant does not
// check it), the counterexample's length when it breaks (0: none known),
// the depth a "holds" was proved to (0: the whole state graph), and
// whether that is as expected.
type MatrixCellDoc struct {
	Variant string `json:"variant"`
	Expect  string `json:"expect,omitempty"`
	Verdict string `json:"verdict,omitempty"`
	Steps   int    `json:"steps,omitempty"`
	Depth   int    `json:"depth,omitempty"`
	OK      bool   `json:"ok"`
}
