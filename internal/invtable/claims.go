package invtable

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/chucklehead-dev/gotogether/internal/nearest"
)

// A Claim is a statement a design document's author marks as one
// (ROADMAP CW3's doc lint): a paragraph, list item or quoted line whose
// text begins with `Claim:` (or `**Claim:**`), through the end of its
// paragraph or item, citing what makes it true:
//
//	Claim: a transfer never spends a balance twice (prop: noDoubleSpend),
//	as long as one caller holds the account (assume: oneCaller).
//
// Prose without the marker is never a claim: the lint reads what the
// author marked, and no heuristic decides what a sentence asserts.
type Claim struct {
	// Path is the document's, Line the marker's
	Path string
	Line int
	// Text is the claim's, its lines joined, the marker taken off
	Text string
	// Props and Assumes are the names its (prop: ...) and (assume: ...)
	// citations give, in order
	Props, Assumes []string
	// empty are the kinds of the citations that name nothing, "(prop: )"
	empty []string
}

// claimStart is a line that opens a claim: up to three spaces, any
// quote markers, a list item's marker, then the marker word;
// blockLine one that opens another block (a heading, a list item, a
// fence, a table row, a quote's own marker alone ends nothing); fenceLine
// a fence's.
var (
	claimStart = regexp.MustCompile(`^ {0,3}(?:>\s?)*(?:(?:[-*+]|\d+[.)])\s+)?(?:\*\*Claim:\*\*|\*\*Claim\*\*:|Claim:)(?:\s+(.*))?$`)
	blockLine  = regexp.MustCompile(`^ {0,3}(?:>\s?)*(?:#{1,6}(?:\s|$)|(?:[-*+]|\d+[.)])\s|\||<!--)`)
	fenceLine  = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})")
	quoteOnly  = regexp.MustCompile(`^\s*(?:>\s?)+\s*$`)
	citation   = regexp.MustCompile(`\((prop|assume):([^)]*)\)`)
	// spaceBefore is a space a citation taken out leaves before
	// punctuation
	spaceBefore = regexp.MustCompile(` ([.,;:])`)
)

// Claims are the claims of Markdown document src (path is how they name
// it), in order. Fenced code is no prose: a marker there (a quint block,
// an example of the syntax) opens nothing.
func Claims(src []byte, path string) []Claim {
	var out []Claim
	var cur *Claim
	fence := ""
	end := func() {
		if cur != nil {
			cur.Text = strings.Join(strings.Fields(cur.Text), " ")
			for _, m := range citation.FindAllStringSubmatch(cur.Text, -1) {
				names := strings.FieldsFunc(m[2], func(r rune) bool { return r == ',' || r == ' ' || r == '\t' })
				if len(names) == 0 {
					cur.empty = append(cur.empty, m[1])
				}
				if m[1] == "prop" {
					cur.Props = append(cur.Props, names...)
				} else {
					cur.Assumes = append(cur.Assumes, names...)
				}
			}
			out = append(out, *cur)
			cur = nil
		}
	}
	for i, line := range strings.Split(string(src), "\n") {
		if fence != "" {
			if t := strings.TrimSpace(line); strings.HasPrefix(t, fence) && strings.Trim(t, fence[:1]) == "" {
				fence = ""
			}
			continue
		}
		if f := fenceLine.FindStringSubmatch(line); f != nil {
			end()
			fence = f[1]
			continue
		}
		if m := claimStart.FindStringSubmatch(line); m != nil {
			end()
			cur = &Claim{Path: path, Line: i + 1, Text: m[1]}
			continue
		}
		if cur == nil {
			continue
		}
		if strings.TrimSpace(line) == "" || quoteOnly.MatchString(line) || blockLine.MatchString(line) {
			end()
			continue
		}
		cur.Text += " " + strings.TrimLeft(strings.TrimSpace(line), "> ")
	}
	end()
	return out
}

// short is a claim's text as messages quote it: its first 60 characters.
func (c Claim) short() string {
	t := citation.ReplaceAllString(c.Text, "")
	t = strings.Join(strings.Fields(t), " ")
	t = spaceBefore.ReplaceAllString(t, "$1")
	if r := []rune(t); len(r) > 60 {
		t = strings.TrimSpace(string(r[:60])) + "..."
	}
	return t
}

// A ClaimCheck is what CheckClaims found of a document's claims.
type ClaimCheck struct {
	// Claims is how many there are; Props and Assumes the distinct
	// properties and assumptions they cite
	Claims, Props, Assumes int
	// Errors are the claims that cite nothing, and the citations that
	// name no property or assumption of the model, each at its claim's
	// file:line with the fix
	Errors []string
	// Unbacked are the cited properties nothing enforces or no test
	// checks, and the cited assumptions nothing targets, each at its
	// claim with the directive to add: failures under -require cited,
	// notes otherwise
	Unbacked []string
}

// CheckClaims checks claims against the table's model: each claim cites
// a property or an assumption, and each it cites is the model's; and
// against its rows: each property cited is enforced and checked, each
// assumption cited targeted (Unbacked).
func (t *Table) CheckClaims(claims []Claim) ClaimCheck {
	m := t.Model
	res := ClaimCheck{Claims: len(claims)}
	props, assumes := map[string]bool{}, map[string]bool{}
	for _, c := range claims {
		at := fmt.Sprintf("%s:%d: claim %q", c.Path, c.Line, c.short())
		for _, k := range c.empty {
			res.Errors = append(res.Errors, fmt.Sprintf("%s: (%s: ) names nothing: give the names it cites, (%s: name, ...)", at, k, k))
		}
		if len(c.Props) == 0 && len(c.Assumes) == 0 {
			if len(c.empty) == 0 {
				res.Errors = append(res.Errors, fmt.Sprintf("%s cites no property or assumption: cite the property that makes it true, (prop: name), or the assumption it rests on, (assume: name); %s; if the model has neither, add it to the model, or the sentence is no claim (drop Claim:)", at, m.citable()))
			}
			continue
		}
		for _, n := range c.Props {
			p, why := m.citedProperty(n)
			if p == nil {
				res.Errors = append(res.Errors, fmt.Sprintf("%s cites property %s: %s", at, n, why))
				continue
			}
			props[p.key()] = true
			i := slices.IndexFunc(t.Rows, func(r Row) bool { return r.Property.key() == p.key() })
			switch {
			case i < 0:
				res.Unbacked = append(res.Unbacked, fmt.Sprintf("%s cites property %s, which this table leaves out (the binding does not check it, or -props, -invariant or -exclude left it out): add it to the selection", at, n))
			case len(t.Rows[i].Enforced) == 0 && len(t.Rows[i].Checked) == 0:
				res.Unbacked = append(res.Unbacked, fmt.Sprintf("%s cites property %s, which nothing enforces and no test checks: put //quint:enforces %s on the code that does, and //quint:checks %s on the test", at, n, p.Name, p.Name))
			case len(t.Rows[i].Enforced) == 0:
				res.Unbacked = append(res.Unbacked, fmt.Sprintf("%s cites property %s, which nothing enforces: put //quint:enforces %s on the code that does", at, n, p.Name))
			case len(t.Rows[i].Checked) == 0:
				res.Unbacked = append(res.Unbacked, fmt.Sprintf("%s cites property %s, which no test checks: put //quint:checks %s on the test that does", at, n, p.Name))
			}
		}
		for _, n := range c.Assumes {
			a, why := m.citedAssumption(n)
			if a == nil {
				res.Errors = append(res.Errors, fmt.Sprintf("%s cites assumption %s: %s", at, n, why))
				continue
			}
			assumes[a.Name] = true
			i := slices.IndexFunc(t.Assumptions, func(r AssumptionRow) bool { return r.Assumption == a })
			if i >= 0 && assumptionMissing(t.Assumptions[i], "targeted") {
				res.Unbacked = append(res.Unbacked, fmt.Sprintf("%s cites assumption %s, which nothing targets: put //quint:exercises %s on a test or double that goes past it, or //quint:monitors %s on the production check that watches it", at, n, a.Name, a.Name))
			}
		}
	}
	res.Props, res.Assumes = len(props), len(assumes)
	return res
}

// propertyNames are the boolean vals and temporals main sees that a
// claim may cite, whatever a selection kept: a claim is about the model,
// not one table of it.
func (m *Model) propertyNames() []string {
	var out []string
	for name, key := range m.visible[m.Main] {
		if m.all[key] != nil && m.predicates[key] == nil {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

// citable names what a claim may cite, for a message.
func (m *Model) citable() string {
	var parts []string
	if ps := m.propertyNames(); len(ps) > 0 {
		parts = append(parts, fmt.Sprintf("model %s's properties are %s", m.Main, strings.Join(ps, ", ")))
	} else {
		parts = append(parts, fmt.Sprintf("model %s has no properties (boolean vals)", m.Main))
	}
	if as := m.assumptionNames(); len(as) > 0 {
		parts = append(parts, "its assumptions "+strings.Join(as, ", "))
	} else {
		parts = append(parts, "it lists no assumptions (// @assume lines)")
	}
	return strings.Join(parts, ", ")
}

// citedProperty is the property a claim's (prop: name) means: a boolean
// val or temporal main sees (bare, or qualified by a module of the spec
// that sees it), selected or not; or why it is none, with the fix.
func (m *Model) citedProperty(name string) (*Property, string) {
	mod, short, qualified := strings.Cut(strings.Replace(name, "::", ".", 1), ".")
	if !qualified {
		mod, short = m.Main, name
	}
	if !ident.MatchString(mod) || !ident.MatchString(short) {
		return nil, "a property is cited by its val's name, or module.name"
	}
	vis, ok := m.visible[mod]
	if !ok {
		return nil, fmt.Sprintf("the spec has no module %s: cite a property as model %s sees it, or by a module of the spec", mod, m.Main)
	}
	key, ok := vis[short]
	if p := m.all[key]; ok && p != nil {
		if a := m.predicates[key]; a != nil {
			return nil, fmt.Sprintf("%s states assumption %s of model %s, which is no property: cite the assumption, (assume: %s)", name, a.Name, m.Main, a.Name)
		}
		if _, seen := m.visible[m.Main][short]; qualified && mod != m.Main && !seen {
			return nil, fmt.Sprintf("model %s does not see %s's %s (it imports it qualified, or not at all): cite a property of model %s", m.Main, mod, short, m.Main)
		}
		return p, ""
	}
	if a, _ := m.resolveAssumption(name); a != nil {
		return nil, fmt.Sprintf("%s is an assumption of model %s (what it leaves out), not a property: cite it as (assume: %s)", name, m.Main, name)
	}
	msg := fmt.Sprintf("model %s has no property %s: ", m.Main, short)
	if ok {
		msg = fmt.Sprintf("%s is no boolean val of model %s (a property is one): ", name, m.Main)
	}
	ps := m.propertyNames()
	if s := nearest.Name(short, ps); s != "" {
		msg += fmt.Sprintf("did you mean %s? ", s)
	}
	if len(ps) == 0 {
		return nil, msg + "it has no properties: state the claim as a val in the model, then cite it"
	}
	return nil, msg + fmt.Sprintf("cite one of %s, or state the claim as a val in the model", strings.Join(ps, ", "))
}

// citedAssumption is the assumption a claim's (assume: name) means, or
// why it is none, with the fix.
func (m *Model) citedAssumption(name string) (*Assumption, string) {
	if a, _ := m.resolveAssumption(name); a != nil {
		return a, ""
	}
	if p, _ := m.citedProperty(name); p != nil {
		return nil, fmt.Sprintf("%s is a property of model %s, not an assumption: cite it as (prop: %s)", name, m.Main, name)
	}
	_, why := m.resolveAssumption(name)
	if why == otherModel {
		mod, _, _ := strings.Cut(strings.Replace(name, "::", ".", 1), ".")
		return nil, fmt.Sprintf("the spec has no module %s: cite an assumption by its @assume line's name", mod)
	}
	if len(m.Assumptions) == 0 {
		return nil, fmt.Sprintf("model %s lists no assumptions: declare it in the model (// @assume %s: what the model leaves out), then cite it", m.Main, name)
	}
	msg := why + ": "
	if s := nearest.Name(name, m.assumptionNames()); s != "" {
		msg += fmt.Sprintf("did you mean %s? ", s)
	}
	return nil, msg + fmt.Sprintf("cite one of model %s's assumptions (%s), or declare it in the model (// @assume %s: what the model leaves out)", m.Main, strings.Join(m.assumptionNames(), ", "), name)
}
