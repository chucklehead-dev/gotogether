package binding

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// instanceModule reports whether module, in the Quint source src, is an
// instance module: a module whose whole body is one import of another
// module, instantiated (`import M(C = expr, ...).*`) or not (`import M.*`).
// It returns the imported module and the instance's "NAME = expr" constants.
// A module with anything beside that import (definitions, a second import)
// is an ordinary module of its own: ok is false. Quint does not re-export
// what an instance module imports, so a binding naming one would find none
// of the model's actions in it (Load binds the imported module instead).
func instanceModule(src, module string) (base string, constants []string, ok bool) {
	src = stripComments(src)
	head := regexp.MustCompile(`(?m)^\s*module\s+` + regexp.QuoteMeta(module) + `\s*\{`).FindStringIndex(src)
	if head == nil {
		return "", nil, false
	}
	body, ok := balanced(src[head[1]:], '{', '}')
	if !ok {
		return "", nil, false
	}
	body = strings.TrimSpace(body)
	m := regexp.MustCompile(`^import\s+([A-Za-z_][A-Za-z0-9_]*)\s*`).FindStringSubmatchIndex(body)
	if m == nil {
		return "", nil, false
	}
	base, rest := body[m[2]:m[3]], body[m[1]:]
	if strings.HasPrefix(rest, "(") {
		args, ok := balanced(rest[1:], '(', ')')
		if !ok {
			return "", nil, false
		}
		rest = strings.TrimSpace(rest[1+len(args)+1:])
		constants = splitTop(args)
	}
	if rest != ".*" {
		return "", nil, false
	}
	return base, constants, true
}

// balanced is s up to the close that ends the open already read, outside
// string literals (Quint strings have no escapes); ok is false if none does.
func balanced(s string, open, close byte) (string, bool) {
	depth, quoted := 1, false
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '"':
			quoted = !quoted
		case quoted:
		case c == open:
			depth++
		case c == close:
			if depth--; depth == 0 {
				return s[:i], true
			}
		}
	}
	return "", false
}

// splitTop splits a constant list at its top-level commas, each part's
// whitespace collapsed; empty parts are dropped.
func splitTop(s string) []string {
	var parts []string
	depth, quoted, start := 0, false, 0
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '"':
			quoted = !quoted
		case quoted:
		case c == '(' || c == '{' || c == '[':
			depth++
		case c == ')' || c == '}' || c == ']':
			depth--
		case c == ',' && depth == 0:
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	parts = append(parts, s[start:])
	var out []string
	for _, p := range parts {
		if p = strings.Join(strings.Fields(p), " "); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// stripComments removes Quint's line and block comments outside strings.
func stripComments(s string) string {
	var b strings.Builder
	quoted := false
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '"':
			quoted = !quoted
			b.WriteByte(s[i])
		case !quoted && strings.HasPrefix(s[i:], "//"):
			for i < len(s) && s[i] != '\n' {
				i++
			}
			b.WriteByte('\n')
		case !quoted && strings.HasPrefix(s[i:], "/*"):
			if end := strings.Index(s[i+2:], "*/"); end >= 0 {
				i += 2 + end + 1
			} else {
				i = len(s)
			}
			b.WriteByte(' ')
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// InstanceConstants reads the constant overrides of an instance module, i.e.
// a module of the form
//
//	module currentDesign {
//	  import edgePublish(PAYLOADS = Set(1, 2), ...).*
//	}
//
// and returns the instantiated module's name and "NAME = expr" overrides.
// (A generated module cannot `import currentDesign.*`: Quint does not
// re-export what an instance module imports.)
func InstanceConstants(spec, instance string) (module string, constants []string, err error) {
	src, err := os.ReadFile(spec)
	if err != nil {
		return "", nil, err
	}
	s := string(src)
	re := regexp.MustCompile(`(?s)module\s+` + regexp.QuoteMeta(instance) + `\s*\{\s*import\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
	loc := re.FindStringSubmatchIndex(s)
	if loc == nil {
		return "", nil, fmt.Errorf("%s: no module %s of the form `module %s { import M(...).* }`", spec, instance, instance)
	}
	module = s[loc[2]:loc[3]]
	i, depth, start := loc[1], 1, loc[1]
	var parts []string
	for ; i < len(s) && depth > 0; i++ {
		switch s[i] {
		case '(', '{', '[':
			depth++
		case ')', '}', ']':
			depth--
		case ',':
			if depth == 1 {
				parts = append(parts, s[start:i])
				start = i + 1
			}
		}
	}
	if depth != 0 {
		return "", nil, fmt.Errorf("%s: unbalanced import in module %s", spec, instance)
	}
	parts = append(parts, s[start:i-1])
	for _, p := range parts {
		if p = strings.Join(strings.Fields(p), " "); p != "" {
			constants = append(constants, p)
		}
	}
	return module, constants, nil
}
