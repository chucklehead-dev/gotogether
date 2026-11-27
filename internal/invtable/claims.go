package invtable

import (
	"cmp"
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
// `Claim (model):` (or `**Claim (model):**`) marks a claim about the
// model alone, which the code need not back (a model statement whose
// runtime lives in another language): it cites as any claim does, and
// its citations are checked against the model, but -require cited does
// not ask the code to enforce, check or target what it cites.
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
	// Model is set for a model-only claim, Claim (model):
	Model bool
	// empty are the kinds of the citations that name nothing, "(prop: )"
	empty []string
	// scope is a marker's parenthesis that is not (model), "" otherwise
	scope string
}

// claimStart is a line that opens a claim: up to three spaces, any
// quote markers, a list item's marker, then the marker word;
// blockLine one that opens another block (a heading, a list item, a
// fence, a table row, a quote's own marker alone ends nothing); fenceLine
// a fence's.
var (
	claimStart = regexp.MustCompile(`^ {0,3}(?:>\s?)*(?:(?:[-*+]|\d+[.)])\s+)?(?:\*\*Claim(?: \(([^)]*)\))?:\*\*|\*\*Claim(?: \(([^)]*)\))?\*\*:|Claim(?: \(([^)]*)\))?:)(?:\s+(.*))?$`)
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
			cur = &Claim{Path: path, Line: i + 1, Text: m[4]}
			if scope := m[1] + m[2] + m[3]; scope == "model" {
				cur.Model = true
			} else if m[1] != "" || m[2] != "" || m[3] != "" || strings.Contains(line, "Claim ()") {
				cur.scope = "(" + scope + ")"
			}
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
	// properties and assumptions of this model they cite
	Claims, Props, Assumes int
	// ModelOnly is how many are claims about the model alone,
	// Claim (model):
	ModelOnly int
	// Elsewhere is how many cite only other models (every citation
	// qualified by a module the spec does not have): left to those
	// models' runs, checked by none here
	Elsewhere int
	// Others count the citations qualified by a module the spec does not
	// have, by that module: another model's, left out as its
	// annotations are
	Others map[string]int
	// Errors are the claims that cite nothing, and the citations that
	// name no property or assumption of the model, each at its claim's
	// file:line with the fix
	Errors []string
	// Unbacked are the cited properties nothing enforces or no test
	// checks, and the cited assumptions nothing targets, each at its
	// claim with the directive to add: failures under -require cited,
	// notes otherwise
	Unbacked []string
	// ModelNotes are what Unbacked would hold of the model-only claims:
	// notes under -require cited too, since such a claim does not say the
	// code backs it
	ModelNotes []string
}

// dropClaim ends the error of a citation naming what the model lacks: a
// claim the document no longer makes (golem's superseded ADR sentence,
// citing a retired assumption) is fixed by its marker, not the model.
const dropClaim = "; or, if the document no longer makes this claim, drop %s"

// CheckClaims checks claims against the table's model: each claim cites
// a property or an assumption, and each it cites is the model's; and
// against its rows: each property cited is enforced and checked, each
// assumption cited targeted (Unbacked; ModelNotes for a model-only
// claim). A citation qualified by a module the spec does not have is
// another model's: counted in Others and left out, and a claim all of
// whose citations are is left to that model's run (Elsewhere).
func (t *Table) CheckClaims(claims []Claim) ClaimCheck {
	m := t.Model
	res := ClaimCheck{Claims: len(claims), Others: map[string]int{}}
	props, assumes := map[string]bool{}, map[string]bool{}
	for _, c := range claims {
		at := fmt.Sprintf("%s:%d: claim %q", c.Path, c.Line, c.short())
		marker := "Claim:"
		if c.Model {
			marker = "Claim (model):"
			res.ModelOnly++
		}
		if c.scope != "" {
			res.Errors = append(res.Errors, fmt.Sprintf("%s: Claim %s: is no marker: write Claim: for a claim the code backs, or Claim (model): for one about the model alone", at, c.scope))
			continue
		}
		for _, k := range c.empty {
			res.Errors = append(res.Errors, fmt.Sprintf("%s: (%s: ) names nothing: give the names it cites, (%s: name, ...)", at, k, k))
		}
		if len(c.Props) == 0 && len(c.Assumes) == 0 {
			if len(c.empty) == 0 {
				res.Errors = append(res.Errors, fmt.Sprintf("%s cites no property or assumption: cite the property that makes it true, (prop: name), or the assumption it rests on, (assume: name); %s; if the model has neither, add it to the model, or the sentence is no claim (drop %s)", at, m.citable(), marker))
			}
			continue
		}
		unbacked := func(msg string) {
			if c.Model {
				res.ModelNotes = append(res.ModelNotes, msg)
			} else {
				res.Unbacked = append(res.Unbacked, msg)
			}
		}
		other := func(name string) {
			mod, _, _ := strings.Cut(strings.Replace(name, "::", ".", 1), ".")
			res.Others[mod]++
		}
		here := 0
		for _, n := range c.Props {
			p, why, unknown := m.citedProperty(n)
			if why == otherModel {
				other(n)
				continue
			}
			here++
			if p == nil {
				if unknown {
					why += fmt.Sprintf(dropClaim, marker)
				}
				res.Errors = append(res.Errors, fmt.Sprintf("%s cites property %s: %s", at, n, why))
				continue
			}
			props[p.key()] = true
			i := slices.IndexFunc(t.Rows, func(r Row) bool { return r.Property.key() == p.key() })
			d := t.Directive(p.Name)
			switch {
			case i < 0:
				msg := fmt.Sprintf("%s cites property %s, which this table leaves out (%s), so no code can be shown to back it: add it to the selection", at, n, cmp.Or(m.left[p.key()], "a selection left it out"))
				if !c.Model {
					msg += ", or mark the claim Claim (model): if it is about the model alone"
				}
				unbacked(msg)
			case len(t.Rows[i].Enforced) == 0 && len(t.Rows[i].Checked) == 0:
				unbacked(fmt.Sprintf("%s cites property %s, which nothing enforces and no test checks: put //quint:enforces %s on the code that does, and //quint:checks %s on the test", at, n, d, d))
			case len(t.Rows[i].Enforced) == 0:
				unbacked(fmt.Sprintf("%s cites property %s, which nothing enforces: put //quint:enforces %s on the code that does", at, n, d))
			case len(t.Rows[i].Checked) == 0:
				unbacked(fmt.Sprintf("%s cites property %s, which no test checks: put //quint:checks %s on the test that does", at, n, d))
			}
		}
		for _, n := range c.Assumes {
			a, why, unknown := m.citedAssumption(n)
			if why == otherModel {
				other(n)
				continue
			}
			here++
			if a == nil {
				if unknown {
					why += fmt.Sprintf(dropClaim, marker)
				}
				res.Errors = append(res.Errors, fmt.Sprintf("%s cites assumption %s: %s", at, n, why))
				continue
			}
			assumes[a.Name] = true
			i := slices.IndexFunc(t.Assumptions, func(r AssumptionRow) bool { return r.Assumption == a })
			if d := t.Directive(a.Name); i >= 0 && assumptionMissing(t.Assumptions[i], "targeted") {
				unbacked(fmt.Sprintf("%s cites assumption %s, which nothing targets: put //quint:exercises %s on a test or double that goes past it, or //quint:monitors %s on the production check that watches it", at, n, d, d))
			}
		}
		if here == 0 {
			res.Elsewhere++
			if c.Model {
				res.ModelOnly--
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
// that sees it), selected or not; or why it is none, with the fix
// (otherModel for a module the spec does not have). unknown is set when
// the model lacks the name, rather than having it as the other kind.
func (m *Model) citedProperty(name string) (p *Property, why string, unknown bool) {
	mod, short, qualified := strings.Cut(strings.Replace(name, "::", ".", 1), ".")
	if !qualified {
		mod, short = m.Main, name
	}
	if !ident.MatchString(mod) || !ident.MatchString(short) {
		return nil, "a property is cited by its val's name, or module.name", false
	}
	vis, ok := m.visible[mod]
	if !ok {
		return nil, otherModel, false
	}
	key, ok := vis[short]
	if p := m.all[key]; ok && p != nil {
		if a := m.predicates[key]; a != nil {
			return nil, fmt.Sprintf("%s states assumption %s of model %s, which is no property: cite the assumption, (assume: %s)", name, a.Name, m.Main, a.Name), false
		}
		if _, seen := m.visible[m.Main][short]; qualified && mod != m.Main && !seen {
			return nil, fmt.Sprintf("model %s does not see %s's %s (it imports it qualified, or not at all): cite a property of model %s", m.Main, mod, short, m.Main), true
		}
		return p, "", false
	}
	if a, _ := m.resolveAssumption(name); a != nil {
		return nil, fmt.Sprintf("%s is an assumption of model %s (what it leaves out), not a property: cite it as (assume: %s)", name, m.Main, name), false
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
		return nil, msg + "it has no properties: state the claim as a val in the model, then cite it", true
	}
	return nil, msg + fmt.Sprintf("cite one of %s, or state the claim as a val in the model", strings.Join(ps, ", ")), true
}

// citedAssumption is the assumption a claim's (assume: name) means, or
// why it is none, with the fix (otherModel for a module the spec does
// not have); unknown as citedProperty's.
func (m *Model) citedAssumption(name string) (a *Assumption, why string, unknown bool) {
	if a, _ := m.resolveAssumption(name); a != nil {
		return a, "", false
	}
	if p, _, _ := m.citedProperty(name); p != nil {
		return nil, fmt.Sprintf("%s is a property of model %s, not an assumption: cite it as (prop: %s)", name, m.Main, name), false
	}
	_, why = m.resolveAssumption(name)
	if why == otherModel {
		return nil, otherModel, false
	}
	if strings.HasPrefix(why, "an assumption is named") {
		return nil, why, false
	}
	if len(m.Assumptions) == 0 {
		return nil, fmt.Sprintf("model %s lists no assumptions: declare it in the model (// @assume %s: what the model leaves out), then cite it", m.Main, name), true
	}
	msg := why + ": "
	if s := nearest.Name(name, m.assumptionNames()); s != "" {
		msg += fmt.Sprintf("did you mean %s? ", s)
	}
	return nil, msg + fmt.Sprintf("cite one of model %s's assumptions (%s), or declare it in the model (// @assume %s: what the model leaves out)", m.Main, strings.Join(m.assumptionNames(), ", "), name), true
}
