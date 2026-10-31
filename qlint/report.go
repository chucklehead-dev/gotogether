package qlint

import "encoding/json"

// Severity is a lint finding's consequence within the inspected scope.
type Severity string

const (
	Error   Severity = "error"
	Warning Severity = "warning"
	Info    Severity = "info"
)

// CheckStatus distinguishes completed checks from checks that could not run.
type CheckStatus string

const (
	CheckPass    CheckStatus = "pass"
	CheckFail    CheckStatus = "fail"
	CheckBlocked CheckStatus = "blocked"
)

// Diagnostic is an actionable finding. Zero location fields mean the check has
// no precise source location; a message is never parsed to invent one.
type Diagnostic struct {
	Code       string   `json:"code"`
	Severity   Severity `json:"severity"`
	Message    string   `json:"message"`
	Path       string   `json:"path,omitempty"`
	Line       int      `json:"line,omitempty"`
	Column     int      `json:"column,omitempty"`
	Symbol     string   `json:"symbol,omitempty"`
	Fix        string   `json:"fix"`
	Confidence string   `json:"confidence"`
}

// Check records one requested inspection, including its dependency failures.
// Operational distinguishes unavailable inputs/tools from authored findings:
// invalid schema is an ordinary finding even when it blocks dependent checks.
type Check struct {
	Name        string      `json:"name"`
	Target      string      `json:"target"`
	Status      CheckStatus `json:"status"`
	Reason      string      `json:"reason,omitempty"`
	Operational bool        `json:"operational,omitempty"`
}

// EvidenceItem identifies an inspected source declaration or literal recording.
type EvidenceItem struct {
	Path   string `json:"path"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
	Symbol string `json:"symbol,omitempty"`
	Action string `json:"action"`
}

// Evidence retains an inventory behind a completed check. Count is the number
// of listed source sites, bound actions, or loaded conversion documents. It is
// not an assertion that all dynamic recording sites were found.
type Evidence struct {
	Kind   string         `json:"kind"`
	Target string         `json:"target"`
	Count  int            `json:"count"`
	Items  []EvidenceItem `json:"items,omitempty"`
}

// Report is a nonwriting lint inspection. It describes only selected checks;
// passing syntactic scans do not establish type correctness or conformance.
// Completeness and exit status are derived from the evidence, never stored flags.
type Report struct {
	Format string `json:"format"`
	// Scope names the claim established by completed checks. Limits retains
	// relevant exclusions for machine consumers as well as human renderers.
	Scope    string       `json:"scope"`
	Limits   []string     `json:"limits"`
	Checks   []Check      `json:"checks"`
	Findings []Diagnostic `json:"findings"`
	Evidence []Evidence   `json:"evidence"`
}

// Complete reports whether every requested check ran to a conclusion.
func (r *Report) Complete() bool {
	if r == nil || len(r.Checks) == 0 {
		return false
	}
	for _, c := range r.Checks {
		if c.Status != CheckPass && c.Status != CheckFail {
			return false
		}
	}
	return true
}

// ExitCode is 0 for clean performed checks, 1 for authored findings, and 2 for
// unavailable inputs/tools or invalid requests. Dependency blocks from ordinary
// findings retain exit 1 and do not conceal independently performed checks.
func (r *Report) ExitCode() int {
	if r == nil || len(r.Checks) == 0 {
		return 2
	}
	bad := false
	for _, c := range r.Checks {
		if c.Operational {
			return 2
		}
		if c.Status != CheckPass {
			bad = true
		}
	}
	for _, f := range r.Findings {
		if f.Severity == Error {
			bad = true
		}
	}
	if bad {
		return 1
	}
	return 0
}

// MarshalJSON includes derived readiness for readers without Go methods.
func (r *Report) MarshalJSON() ([]byte, error) {
	type fields Report
	return json.Marshal(struct {
		*fields
		Complete bool `json:"complete"`
		ExitCode int  `json:"exitCode"`
	}{(*fields)(r), r.Complete(), r.ExitCode()})
}
