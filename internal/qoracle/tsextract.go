package qoracle

import (
	"strconv"
	"strings"
)

// The inline programs of quint's unit tests (quint/test/**/*.test.ts): a
// program there is a list of declaration strings, which the tests' builders
// wrap as `module wrapper { ... }` (test/builders/ir.ts,
// buildModuleWithDecls), or a whole module's text. ExtractPrograms finds
// them in a test file's source without running it:
//   - an array literal of string literals whose first string starts a
//     declaration (`const defs = ['val a = 1', ...]`), wrapped as the
//     builders wrap it; one passed to buildModuleWithExpressions is
//     expressions, each `def d<i> = <e>` as buildModule writes them;
//   - a string literal starting `module `: a whole program;
//   - a string literal passed to buildDef or buildDecl: one declaration.
// A template literal with a substitution (`${`) is skipped: its text is
// the test's to build.

type tsToken struct {
	kind byte // 's' a string literal, 'i' an identifier, 'p' punctuation
	text string
	ok   bool // a string literal with no substitution
}

func tsTokens(src string) []tsToken {
	var out []tsToken
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			end := strings.Index(src[i+2:], "*/")
			if end < 0 {
				return out
			}
			i += end + 4
		case c == '\'' || c == '"' || c == '`':
			s, n, ok := tsString(src[i:])
			out = append(out, tsToken{'s', s, ok})
			i += n
		case c == '_' || c == '$' || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z'):
			j := i
			for j < len(src) && (src[j] == '_' || src[j] == '$' || ('a' <= src[j] && src[j] <= 'z') || ('A' <= src[j] && src[j] <= 'Z') || ('0' <= src[j] && src[j] <= '9')) {
				j++
			}
			out = append(out, tsToken{'i', src[i:j], true})
			i = j
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		default:
			out = append(out, tsToken{'p', string(c), true})
			i++
		}
	}
	return out
}

// tsString reads the string literal at the start of s: its value, its
// length in s, and whether it is a plain one (no substitution).
func tsString(s string) (string, int, bool) {
	q := s[0]
	var b strings.Builder
	ok := true
	i := 1
	for i < len(s) {
		c := s[i]
		switch {
		case c == '\\' && i+1 < len(s):
			switch s[i+1] {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			default:
				b.WriteByte(s[i+1])
			}
			i += 2
			continue
		case c == q:
			return b.String(), i + 1, ok
		case q == '`' && c == '$' && i+1 < len(s) && s[i+1] == '{':
			ok = false
		case q != '`' && c == '\n':
			return b.String(), i, false // unterminated
		}
		b.WriteByte(c)
		i++
	}
	return b.String(), i, false
}

// declKeywords start a Quint declaration (read-only).
var declKeywords = []string{"const ", "var ", "val ", "def ", "pure ", "action ", "run ", "temporal ", "nondet ", "type ", "import ", "export ", "assume "}

func startsDecl(s string) bool {
	s = strings.TrimSpace(s)
	for _, k := range declKeywords {
		if strings.HasPrefix(s, k) {
			return true
		}
	}
	return false
}

// ExtractPrograms is the Quint programs of a quint unit test's source, as
// its builders would build them, each once.
func ExtractPrograms(src string) []string {
	toks := tsTokens(src)
	var out []string
	seen := map[string]bool{}
	add := func(p string) {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	callee := func(i int) string {
		// the identifier before the "(" before token i
		if i >= 2 && toks[i-1].kind == 'p' && toks[i-1].text == "(" && toks[i-2].kind == 'i' {
			return toks[i-2].text
		}
		return ""
	}
	inArray := map[int]bool{}
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		if t.kind == 'p' && t.text == "[" {
			// an array literal of string literals only
			var strs []string
			j := i + 1
			plain := true
			for j < len(toks) {
				if toks[j].kind == 's' {
					strs = append(strs, toks[j].text)
					plain = plain && toks[j].ok
					inArray[j] = true
					j++
					if j < len(toks) && toks[j].kind == 'p' && toks[j].text == "," {
						j++
						continue
					}
				}
				break
			}
			if j < len(toks) && toks[j].kind == 'p' && toks[j].text == "]" && len(strs) > 0 && plain {
				switch {
				case callee(i) == "buildModuleWithExpressions":
					var ds []string
					for k, e := range strs {
						ds = append(ds, "def d"+strconv.Itoa(k)+" = "+e)
					}
					add(wrap(ds))
				case startsDecl(strs[0]):
					add(wrap(strs))
				}
			}
			continue
		}
		if t.kind == 's' && t.ok && !inArray[i] {
			s := strings.TrimSpace(t.text)
			switch {
			case strings.HasPrefix(s, "module "):
				add(t.text)
			case (callee(i) == "buildDef" || callee(i) == "buildDecl") && startsDecl(s):
				add(wrap([]string{t.text}))
			}
		}
	}
	return out
}

// wrap is the builders' module (test/builders/ir.ts, buildModuleWithDecls).
func wrap(decls []string) string {
	return "module wrapper { " + strings.Join(decls, "\n") + " }"
}
