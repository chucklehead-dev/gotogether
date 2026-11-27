package invtable

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/chucklehead-dev/gotogether/internal/nearest"
)

// AssumptionsBegin and AssumptionsEnd bound the place in a document where
// `gotogether invariants -write` writes the table of the model's listed
// assumptions (ROADMAP CW3).
const (
	AssumptionsBegin = "<!-- gotogether:assumptions -->"
	AssumptionsEnd   = "<!-- /gotogether:assumptions -->"
)

// The kinds of the assumption directives: code that goes past an
// assumption on purpose (a test, a double's fault, a sweep row, a
// generator aimed beyond it), and a production check that watches it.
const (
	Exercises Kind = "exercises"
	Monitors  Kind = "monitors"
)

// An Assumption is what a model says it leaves out: a line of its own in
// the model's source,
//
//	// @assume unboundedQueue: the queue never fills; the real one caps at 10k
//	// @assume oneSigningKey = singleKey: one key signs every document
//
// a plain `//` comment, so quint reads the model unchanged (a `///` one
// would join the next definition's doc, and quint refuses one at a
// module's end). The name is the assumption's own, distinct from every
// name the main module sees, so a directive naming it cannot mean a
// property; `= val` names the boolean val that states it (a state inside
// the assumption satisfies it), which is then no property of the
// default table.
type Assumption struct {
	// Name is the assumption's
	Name string
	// Module is the module whose source holds the line
	Module string
	// Statement is the text after the colon: what the model leaves out
	Statement string
	// Predicate is the boolean val that states it, "" for prose alone
	Predicate string
	// Line is the line's in the source read
	Line int
}

// An AssumptionRow is an assumption's: the code that exercises it and
// the checks that monitor it.
type AssumptionRow struct {
	Assumption *Assumption
	// Exercised are //quint:exercises directives
	Exercised []Site
	// Monitored are //quint:monitors directives
	Monitored []Site
}

// assumeTag finds the tag in a comment, wherever it is; assumeLine is a
// line that is the tag's comment alone; assumeArgs its arguments.
var (
	assumeTag  = regexp.MustCompile(`//(/?)\s*@assume\b`)
	assumeLine = regexp.MustCompile(`^\s*//(/?)\s*@assume\b(.*)$`)
	assumeArgs = regexp.MustCompile(`^\s+([A-Za-z_][A-Za-z0-9_]*)(?:\s*=\s*([A-Za-z_][A-Za-z0-9_]*))?\s*:\s*(\S.*?)\s*$`)
)

// Assume reads the model's assumptions from its source src (path is how
// problems name the file): each `// @assume` line of a module the main
// module is or sees (another model's in the same file left out), in the
// source's order. It takes each one's predicate out of the default
// properties (a selection that names it keeps it). problems are the lines
// that declare no assumption the table can show, each at its line with
// the fix.
func (m *Model) Assume(src []byte, path string) (problems []string) {
	seen := func(mod string) bool {
		if mod == m.Main {
			return true
		}
		for _, key := range m.visible[m.Main] {
			if strings.HasPrefix(key, mod+"::") {
				return true
			}
		}
		return false
	}
	m.Assumptions, m.predicates = nil, map[string]*Assumption{}
	mod := ""
	for i, line := range strings.Split(string(src), "\n") {
		n := i + 1
		if mm := moduleLine.FindStringSubmatch(line); mm != nil {
			mod = mm[1]
		}
		at := fmt.Sprintf("%s:%d", path, n)
		g := assumeLine.FindStringSubmatch(line)
		if g == nil {
			if assumeTag.MatchString(line) {
				problems = append(problems, at+": @assume trails code: put it on a line of its own, a // comment (// @assume name: what the model leaves out)")
			}
			continue
		}
		if !seen(mod) {
			continue
		}
		if g[1] == "/" {
			problems = append(problems, at+": /// @assume is a doc comment, which quint joins to the next definition's (and refuses at a module's end): write it with // (// @assume name: what the model leaves out)")
			continue
		}
		args := assumeArgs.FindStringSubmatch(g[2])
		if args == nil {
			problems = append(problems, at+": @assume declares no assumption: give its name, a colon and what the model leaves out (// @assume unboundedQueue: the queue never fills; the real one caps at 10k), or name the val that states it (// @assume unboundedQueue = queueUnderCap: ...)")
			continue
		}
		a := &Assumption{Name: args[1], Predicate: args[2], Statement: args[3], Module: mod, Line: n}
		if i := slices.IndexFunc(m.Assumptions, func(b *Assumption) bool { return b.Name == a.Name }); i >= 0 {
			problems = append(problems, fmt.Sprintf("%s: @assume %s is declared again (first at line %d): give each assumption its own name", at, a.Name, m.Assumptions[i].Line))
			continue
		}
		if key, ok := m.visible[m.Main][a.Name]; ok {
			problems = append(problems, fmt.Sprintf("%s: @assume %s has the name of model %s's definition %s: rename the assumption, so that a directive naming it cannot mean a property (%sAssumed, say)", at, a.Name, m.Main, key, a.Name))
			continue
		}
		if a.Predicate != "" {
			p := m.all[m.visible[mod][a.Predicate]]
			if p == nil {
				problems = append(problems, fmt.Sprintf("%s: @assume %s = %s: %s is no boolean val that module %s and model %s see: name the val that states the assumption, or drop = %s for an assumption in prose alone", at, a.Name, a.Predicate, a.Predicate, mod, m.Main, a.Predicate))
				continue
			}
			if b := m.predicates[p.key()]; b != nil {
				problems = append(problems, fmt.Sprintf("%s: @assume %s = %s: %s states assumption %s already (line %d): one val states one assumption", at, a.Name, a.Predicate, a.Predicate, b.Name, b.Line))
				continue
			}
			a.Predicate = p.Name
			m.predicates[p.key()] = a
		}
		m.Assumptions = append(m.Assumptions, a)
	}
	if len(m.predicates) > 0 {
		m.keep(slices.DeleteFunc(slices.Clone(m.Properties), func(p *Property) bool { return m.predicates[p.key()] != nil }), "it states an assumption")
	}
	return problems
}

func (m *Model) assumptionNames() []string {
	var out []string
	for _, a := range m.Assumptions {
		out = append(out, a.Name)
	}
	return out
}

// resolveAssumption is the assumption name means, or why it is none and
// what to do: bare, or qualified (with . or ::) by the module that
// declares it, the main module, or a module that sees the declaring one.
func (m *Model) resolveAssumption(name string) (*Assumption, string) {
	mod, short, qualified := strings.Cut(strings.Replace(name, "::", ".", 1), ".")
	if !qualified {
		mod, short = m.Main, name
	}
	if !ident.MatchString(mod) || !ident.MatchString(short) {
		return nil, "an assumption is named as its @assume line names it, or module.name"
	}
	vis, ok := m.visible[mod]
	if !ok {
		return nil, otherModel
	}
	for _, a := range m.Assumptions {
		if a.Name != short {
			continue
		}
		if mod == a.Module || mod == m.Main {
			return a, ""
		}
		for _, key := range vis {
			if strings.HasPrefix(key, a.Module+"::") {
				return a, ""
			}
		}
		return nil, fmt.Sprintf("module %s does not see module %s, which declares assumption %s", mod, a.Module, short)
	}
	if m.all[vis[short]] != nil {
		return nil, fmt.Sprintf("%s is a property of model %s, not an assumption: //quint:enforces and //quint:checks name properties, //quint:exercises and //quint:monitors what the model leaves out (its // @assume lines)", name, m.Main)
	}
	return nil, fmt.Sprintf("model %s has no assumption %s", m.Main, short)
}

// assumptionKind reports whether a directive names assumptions.
func assumptionKind(k Kind) bool { return k == Exercises || k == Monitors }

// placeAssumption puts an Exercises or Monitors directive in its
// assumption's rows; errs name each name that is none, with the fix.
func (m *Model) placeAssumption(a Annotation, rows map[*Assumption]*AssumptionRow, others map[string]int) (errs []string) {
	for _, n := range a.Names {
		as, why := m.resolveAssumption(n)
		if why == otherModel {
			mod, _, _ := strings.Cut(strings.Replace(n, "::", ".", 1), ".")
			others[mod]++
			continue
		}
		if as == nil {
			msg := fmt.Sprintf("%s:%d: //quint:%s names %s: %s: ", a.Site.Path, a.Line, a.Kind, n, why)
			switch {
			case strings.Contains(why, "is a property of model"):
				msg = strings.TrimSuffix(msg, ": ") + ": name the assumption it goes past"
				if len(m.Assumptions) > 0 {
					msg += fmt.Sprintf(" (%s), or use //quint:enforces or //quint:checks", strings.Join(m.assumptionNames(), ", "))
				}
			case len(m.Assumptions) == 0:
				msg += fmt.Sprintf("model %s lists no assumptions: declare it in the model (// @assume %s: what the model leaves out), or name another module with -main", m.Main, n)
			default:
				if s := nearest.Name(n, m.assumptionNames()); s != "" {
					msg += fmt.Sprintf("did you mean %s? ", s)
				}
				msg += fmt.Sprintf("name one of model %s's assumptions (%s), or declare it in the model (// @assume %s: what the model leaves out)", m.Main, strings.Join(m.assumptionNames(), ", "), n)
			}
			errs = append(errs, msg)
			continue
		}
		r := rows[as]
		if a.Kind == Exercises {
			r.Exercised = appendSite(r.Exercised, a.Site)
		} else {
			r.Monitored = appendSite(r.Monitored, a.Site)
		}
	}
	return errs
}

// assumptionMissing reports whether row r has nothing of kind
// (exercised, monitored, targeted: either).
func assumptionMissing(r AssumptionRow, kind string) bool {
	switch kind {
	case "exercised":
		return len(r.Exercised) == 0
	case "monitored":
		return len(r.Monitored) == 0
	case "targeted":
		return len(r.Exercised) == 0 && len(r.Monitored) == 0
	}
	return false
}

// assumptionWarnings are the assumptions with nothing of the kinds want
// admits, each with the directive to add.
func (t *Table) assumptionWarnings(want func(string) bool) []string {
	var out []string
	for _, r := range t.Assumptions {
		n, d := r.Assumption.Name, t.Directive(r.Assumption.Name)
		if want("targeted") && assumptionMissing(r, "targeted") {
			out = append(out, fmt.Sprintf("assumption %s: nothing targets it: put //quint:exercises %s on a test or double that goes past it, or //quint:monitors %s on the production check that watches it", n, d, d))
			continue
		}
		if want("exercised") && assumptionMissing(r, "exercised") {
			out = append(out, fmt.Sprintf("assumption %s: nothing exercises it: put //quint:exercises %s on a test, a double's fault or a generator that goes past it", n, d))
		}
		if want("monitored") && assumptionMissing(r, "monitored") {
			out = append(out, fmt.Sprintf("assumption %s: nothing monitors it: put //quint:monitors %s on the production check that watches it", n, d))
		}
	}
	return out
}

// MarkdownAssumptions is the table of assumptions, paths relative to dir
// as in Markdown. The predicate column is there when an assumption names
// one.
func (t *Table) MarkdownAssumptions(dir string) string {
	pred := slices.ContainsFunc(t.Assumptions, func(r AssumptionRow) bool { return r.Assumption.Predicate != "" })
	var b strings.Builder
	if pred {
		b.WriteString("| assumption | statement | stated by | exercised by | monitored by |\n|---|---|---|---|---|\n")
	} else {
		b.WriteString("| assumption | statement | exercised by | monitored by |\n|---|---|---|---|\n")
	}
	for _, r := range t.Assumptions {
		a := r.Assumption
		fmt.Fprintf(&b, "| `%s` | %s |", a.Name, strings.ReplaceAll(a.Statement, "|", `\|`))
		if pred {
			if a.Predicate != "" {
				fmt.Fprintf(&b, " `%s` |", a.Predicate)
			} else {
				b.WriteString(" — |")
			}
		}
		fmt.Fprintf(&b, " %s | %s |\n", t.siteCell(r.Exercised, dir), t.siteCell(r.Monitored, dir))
	}
	return b.String()
}
