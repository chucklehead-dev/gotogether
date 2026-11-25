package qoracle

import (
	"context"
	"strings"

	"github.com/chucklehead-dev/gotogether/internal/ddmin"
)

// Shrinking a disagreement (§4.3): qshrink is shaped for a driver's
// recorded actions, so what Q3 reuses is its search, internal/ddmin,
// over a program's declarations (Decls, ShrinkDecls); a generated
// program shrinks by its tree as well (ShrinkModule, gen.go). A candidate
// still fails when the front ends still disagree in the same tier at the
// same canonical path.

// Decls is a Quint text cut into its modules' declarations: every other
// byte (module headers, braces, text between modules) is kept as is.
type Decls struct {
	text  string
	spans [][2]int // each declaration's [start, end) in text
}

// declStart are the keywords that begin a declaration in a module's body
// (read-only).
var declStart = map[string]bool{
	"const": true, "var": true, "assume": true, "val": true, "def": true, "pure": true, "action": true,
	"run": true, "temporal": true, "nondet": true, "import": true, "export": true, "type": true,
}

// SplitDecls cuts text into declarations: at a module body's own depth,
// each declaration keyword starts one (`pure def` is one). A let written
// without braces (`def f = val y = 1 y`) is cut there too: the candidate
// then fails to parse, which a shrink takes as a pass, never a result.
func SplitDecls(text string) *Decls {
	d := &Decls{text: text}
	depth := 0
	inModule := false // inside a module's braces
	moduleDepth := 0
	start := -1
	afterPure := false
	closeDecl := func(at int) {
		if start >= 0 {
			end := at
			for end > start && (text[end-1] == ' ' || text[end-1] == '\n' || text[end-1] == '\t' || text[end-1] == '\r') {
				end--
			}
			d.spans = append(d.spans, [2]int{start, end})
			start = -1
		}
	}
	expectModuleBrace := false
	for i := 0; i < len(text); {
		c := text[i]
		switch {
		case c == '"':
			j := i + 1
			for j < len(text) && text[j] != '"' {
				if text[j] == '\\' {
					j++
				}
				j++
			}
			i = j + 1
			continue
		case c == '/' && i+1 < len(text) && text[i+1] == '/':
			for i < len(text) && text[i] != '\n' {
				i++
			}
			continue
		case c == '/' && i+1 < len(text) && text[i+1] == '*':
			end := strings.Index(text[i+2:], "*/")
			if end < 0 {
				i = len(text)
			} else {
				i += end + 4
			}
			continue
		case c == '{' || c == '(' || c == '[':
			depth++
			if c == '{' && expectModuleBrace {
				inModule, moduleDepth, expectModuleBrace = true, depth, false
			}
		case c == '}' || c == ')' || c == ']':
			if inModule && depth == moduleDepth && c == '}' {
				closeDecl(i)
				inModule = false
			}
			depth--
		case isIdentStart(c):
			j := i
			for j < len(text) && isIdent(text[j]) {
				j++
			}
			// a name of a qualified name (M::val) is no keyword
			word := text[i:j]
			qualified := (i >= 2 && text[i-2:i] == "::") || (j+1 < len(text) && text[j:j+2] == "::")
			switch {
			case !inModule && depth == 0 && word == "module":
				expectModuleBrace = true
			case inModule && depth == moduleDepth && declStart[word] && !qualified:
				if afterPure && (word == "val" || word == "def") {
					afterPure = false
				} else {
					closeDecl(i)
					start = i
					afterPure = word == "pure"
				}
			}
			i = j
			continue
		}
		i++
	}
	closeDecl(len(text))
	return d
}

func isIdentStart(c byte) bool {
	return c == '_' || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z')
}

func isIdent(c byte) bool { return isIdentStart(c) || ('0' <= c && c <= '9') }

// N is the number of declarations.
func (d *Decls) N() int { return len(d.spans) }

// Keep is the text with only the declarations of set (indices, ascending):
// a removed declaration's bytes become spaces, its newlines kept, so that
// every kept node keeps its position.
func (d *Decls) Keep(set []int) string {
	keep := map[int]bool{}
	for _, i := range set {
		keep[i] = true
	}
	b := []byte(d.text)
	for i, s := range d.spans {
		if keep[i] {
			continue
		}
		for k := s[0]; k < s[1]; k++ {
			if b[k] != '\n' {
				b[k] = ' '
			}
		}
	}
	return string(b)
}

// Same is a candidate's test: whether it disagrees as the original does.
func Same(orig *Disagreement, got *Disagreement) bool {
	return got != nil && got.Tier == orig.Tier && got.Path() == orig.Path()
}

// ShrinkDecls is p's text with as few declarations as still disagree as
// orig does (a ddmin over them, 1-minimal), and the candidates tried.
// A candidate whose run fails (a tool failure) is unknown, never a pass.
func ShrinkDecls(ctx context.Context, p Program, text string, orig *Disagreement, a, b FrontEnd, tmp string) (string, int) {
	d := SplitDecls(text)
	if d.N() < 2 {
		return text, 0
	}
	kept, res := MinimizeDecls(d, func(text string) ddmin.Outcome {
		q := p
		q.Text = text
		got, _, err := CompareKnown(ctx, q, a, b, tmp)
		switch {
		case err != nil:
			return ddmin.Unknown
		case Same(orig, got):
			return ddmin.Fails
		}
		return ddmin.Passes
	}, func() bool { return ctx.Err() != nil })
	return kept, res.Tests
}

// MinimizeDecls is ddmin over d's declarations: the text of the 1-minimal
// set test fails on (the whole text taken to fail), and the search's result.
func MinimizeDecls(d *Decls, test func(text string) ddmin.Outcome, stop func() bool) (string, ddmin.Result) {
	res := ddmin.Minimize(d.N(), func(set []int) ddmin.Outcome { return test(d.Keep(set)) }, stop)
	return d.Keep(res.Set), res
}
