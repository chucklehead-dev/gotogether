package binding

import "fmt"

// LintFinding is an actionable static binding or source finding. A missing
// location means the rule has no exact source position; model metadata checks
// do not claim to establish trace conformance.
type LintFinding struct {
	Code       string `json:"code"`
	Severity   string `json:"severity"`
	Message    string `json:"message"`
	Path       string `json:"path,omitempty"`
	Line       int    `json:"line,omitempty"`
	Column     int    `json:"column,omitempty"`
	Symbol     string `json:"symbol,omitempty"`
	Fix        string `json:"fix"`
	Confidence string `json:"confidence"`
	position   string // original scanner presentation, for older in-memory callers supplying only Pos
}

func lintMessages(fs []LintFinding) []string {
	var out []string
	for _, f := range fs {
		out = append(out, lintMessage(f))
	}
	return out
}

func splitLintMessages(fs []LintFinding) (errs, warns []string) {
	for _, f := range fs {
		if f.Severity == "warning" {
			warns = append(warns, lintMessage(f))
		} else {
			errs = append(errs, lintMessage(f))
		}
	}
	return
}

func sourceFinding(code, severity, message, path string, line, column int, symbol, fix string, position ...string) LintFinding {
	f := LintFinding{Code: code, Severity: severity, Message: message, Path: path, Line: line, Column: column, Symbol: symbol, Fix: fix, Confidence: "syntax"}
	if path == "" && len(position) > 0 {
		f.position = position[0]
	}
	return f
}

// lintMessage is only a text renderer. Structured Message never embeds source location.
func lintMessage(f LintFinding) string {
	pos := f.Path
	if pos != "" && f.Line > 0 {
		pos += fmt.Sprintf(":%d", f.Line)
		if f.Column > 0 {
			pos += fmt.Sprintf(":%d", f.Column)
		}
	}
	if pos == "" {
		pos = f.position
	}
	if pos != "" {
		return pos + ": " + f.Message
	}
	return f.Message
}
