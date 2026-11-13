package lint

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/chucklehead-dev/gotogether/internal/quintcmd"
	"github.com/chucklehead-dev/gotogether/internal/quintnames"
)

// A model lint reads a Quint file with no binding: the design-phase model a
// project writes before any code (golem's first use, #144). Its source
// checks run before quint and name what quint reports at length or late:
// a primed assignment whose right side holds a bare `or`/`and` (`x' = x or
// y` is `(x' = x) or y`, which quint reports as a temporal operator in an
// action), and a declaration or parameter named as a built-in or keyword.
// Its typecheck turns quint's effect-unification dump for actions that
// assign different variables into the variables each side assigns.

// qtoken is a Quint token: an identifier (a primed one keeps its quote), or
// one punctuation character; comments and strings are skipped.
type qtoken struct {
	text      string
	line, col int
}

func quintTokens(src string) []qtoken {
	var out []qtoken
	line, col := 1, 1
	advance := func(r byte) {
		if r == '\n' {
			line, col = line+1, 1
		} else {
			col++
		}
	}
	for i := 0; i < len(src); {
		c := src[i]
		switch {
		case strings.HasPrefix(src[i:], "//"):
			for i < len(src) && src[i] != '\n' {
				advance(src[i])
				i++
			}
		case strings.HasPrefix(src[i:], "/*"):
			end := strings.Index(src[i+2:], "*/")
			stop := len(src)
			if end >= 0 {
				stop = i + 2 + end + 2
			}
			for ; i < stop; i++ {
				advance(src[i])
			}
		case c == '"':
			advance(c)
			i++
			for i < len(src) && src[i] != '"' {
				advance(src[i])
				i++
			}
			if i < len(src) {
				advance(src[i])
				i++
			}
		case c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z':
			start, l, k := i, line, col
			for i < len(src) && (src[i] == '_' || src[i] >= 'a' && src[i] <= 'z' || src[i] >= 'A' && src[i] <= 'Z' || src[i] >= '0' && src[i] <= '9') {
				advance(src[i])
				i++
			}
			if i < len(src) && src[i] == '\'' {
				advance(src[i])
				i++
			}
			out = append(out, qtoken{src[start:i], l, k})
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			advance(c)
			i++
		default:
			out = append(out, qtoken{string(c), line, col})
			advance(c)
			i++
		}
	}
	return out
}

// declarations introduce a name: the token after each. Read-only.
var declarations = map[string]bool{"var": true, "const": true, "def": true, "val": true, "action": true, "run": true, "temporal": true, "nondet": true, "type": true}

// lineEnders start a new declaration: a primed assignment with no bracket
// open ends before the next line that begins with one. Read-only.
var lineEnders = map[string]bool{"var": true, "const": true, "def": true, "val": true, "action": true, "run": true, "temporal": true, "pure": true, "type": true, "import": true, "export": true, "module": true, "assume": true}

// modelSourceFindings are the source checks of the Quint file at path.
func modelSourceFindings(path string, src string) []Diagnostic {
	toks := quintTokens(src)
	var out []Diagnostic
	for i, t := range toks {
		// a primed assignment: x' = ..., not x' == ...
		if strings.HasSuffix(t.text, "'") && i+1 < len(toks) && toks[i+1].text == "=" && (i+2 >= len(toks) || toks[i+2].text != "=") {
			depth := 0
			for j := i + 2; j < len(toks); j++ {
				u := toks[j]
				if depth == 0 && u.line != t.line && lineEnders[u.text] && (j == 0 || toks[j-1].line != u.line) {
					break
				}
				switch u.text {
				case "(", "{", "[":
					depth++
					continue
				case ")", "}", "]":
					depth--
				case ",":
					if depth == 0 {
						j = len(toks)
						continue
					}
				}
				if depth < 0 {
					break
				}
				if depth == 0 && (u.text == "or" || u.text == "and" || u.text == "implies" || u.text == "iff") && toks[j-1].text != "." {
					name := strings.TrimSuffix(t.text, "'")
					out = append(out, Diagnostic{Code: "model.assignment.precedence", Severity: Error, Path: path, Line: t.line, Column: t.col, Confidence: "syntax",
						Message: fmt.Sprintf("%s' = ... %s ... reads as (%s' = ...) %s ...: an assignment binds tighter than %s, so quint reports a temporal operator in an action (QNT200)", name, u.text, name, u.text, u.text),
						Fix:     fmt.Sprintf("parenthesize the right side: %s' = (... %s ...)", name, u.text)})
					break
				}
			}
		}
		// a declared name or a parameter quint refuses
		if declarations[t.text] && i+1 < len(toks) {
			if n := toks[i+1]; reservedIdent(n.text) {
				out = append(out, reservedFinding(path, n, t.text))
			}
			// parameters: def/action/temporal NAME(p: T, ...)
			if (t.text == "def" || t.text == "action" || t.text == "temporal" || t.text == "run") && i+2 < len(toks) && toks[i+2].text == "(" {
				depth := 0
				for j := i + 2; j < len(toks); j++ {
					switch toks[j].text {
					case "(", "[", "{":
						depth++
					case ")", "]", "}":
						depth--
					}
					if depth == 0 {
						break
					}
					if depth == 1 && j+1 < len(toks) && toks[j+1].text == ":" && (toks[j-1].text == "(" || toks[j-1].text == ",") && reservedIdent(toks[j].text) {
						out = append(out, reservedFinding(path, toks[j], "parameter"))
					}
				}
			}
		}
	}
	return out
}

func reservedIdent(s string) bool {
	return s != "" && !strings.HasSuffix(s, "'") && quintnames.Reserved(s)
}

func reservedFinding(path string, t qtoken, kind string) Diagnostic {
	return Diagnostic{Code: "model.name.reserved", Severity: Error, Path: path, Line: t.line, Column: t.col, Confidence: "syntax",
		Message: fmt.Sprintf("%s %s: quint refuses %q, a built-in operator or keyword, as a name (QNT101 or a parse error)", kind, t.text, t.text),
		Fix:     fmt.Sprintf("rename it (%s), as gotogether's generator does", quintnames.Rename(t.text))}
}

// unification is quint's report of two effects that assign different
// variables: "Expected [a] and [a,b] to be the same". Read-only.
var unification = regexp.MustCompile(`Error \[QNT000\]: Expected \[([^\]]*)\] and \[([^\]]*)\] to be the same(?s:.*?)\n\s*at (\S+):(\d+):(\d+)`)

// modelTypecheck typechecks path with quint, and turns its effect
// unification errors into the variables each side assigns.
func modelTypecheck(ctx context.Context, quint, path string) ([]Diagnostic, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(abs); err != nil {
		return nil, err
	}
	out, err := quintcmd.Command(ctx, quint, filepath.Dir(abs), "typecheck", abs).CombinedOutput()
	if err == nil {
		return nil, nil
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	text := string(out)
	var ds []Diagnostic
	for _, m := range unification.FindAllStringSubmatch(text, -1) {
		a, b := names(m[1]), names(m[2])
		line, col := 0, 0
		fmt.Sscan(m[4], &line)
		fmt.Sscan(m[5], &col)
		ds = append(ds, Diagnostic{Code: "model.assignment.mismatch", Severity: Error, Path: path, Line: line, Column: col, Confidence: "typecheck",
			Message: fmt.Sprintf("actions combined here assign different variables: one assigns %s, another %s (quint's QNT000 effect unification)", a, b),
			Fix:     "every action combined by any { } or all { } must assign the same variables: give the one that leaves a variable unchanged its x' = x"})
	}
	if len(ds) == 0 {
		first := strings.TrimSpace(text)
		if i := strings.Index(first, "\n\n"); i > 0 {
			first = first[:i]
		}
		ds = append(ds, Diagnostic{Code: "model.typecheck", Severity: Error, Path: path, Message: first, Fix: "fix the model as quint says", Confidence: "typecheck"})
	}
	return ds, nil
}

func names(list string) string {
	var ns []string
	for _, n := range strings.Split(list, ",") {
		if n = strings.Trim(strings.TrimSpace(n), "'"); n != "" {
			ns = append(ns, n)
		}
	}
	if len(ns) == 0 {
		return "no variable"
	}
	return strings.Join(ns, ", ")
}
