package invtable

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
)

// claimsDoc marks claims every way the syntax allows, and leaves prose,
// fenced code and markers mid-line as no claim.
const claimsDoc = "# Design\n" +
	"\n" +
	"Claim: a paragraph's claim (prop: a), its citation\n" + // 3
	"on the next line (prop: m.b, c) and (assume:\n" +
	"x).\n" +
	"This line ends nothing; the blank one does.\n" +
	"\n" +
	"- **Claim:** a list item (assume: y)\n" + // 8
	"  continued.\n" +
	"- the next item ends it (prop: notCited)\n" +
	"1. Claim: numbered (prop: d)\n" + // 11
	"> **Claim**: quoted\n" + // 12
	"> on two lines (prop: e)\n" +
	">\n" +
	"> outside it (prop: notCited)\n" +
	"Claim: no citation at all\n" + // 16
	"## a heading ends it (prop: notCited)\n" +
	"Claim: empty (prop: ) and (assume:)\n" + // 18
	"| a table row ends it (prop: notCited) |\n" +
	"\n" +
	"Prose with Claim: mid-line is none (prop: notCited).\n" +
	"Claims: is another word.\n" +
	"\n" +
	"```markdown\n" +
	"Claim: in a fence (prop: notCited)\n" +
	"```\n" +
	"~~~~\n" +
	"Claim: in another (prop: notCited)\n" +
	"```\n" +
	"~~~~\n" +
	"    Claim: four spaces is code (prop: notCited)\n"

// Claims reads each marked claim through its paragraph or item, with
// its citations, at the marker's line; nothing unmarked or fenced is
// one, and every block that opens ends the claim.
func TestClaims(t *testing.T) {
	cs := Claims([]byte(claimsDoc), "d.md")
	type got struct {
		Line           int
		Text           string
		Props, Assumes []string
		Empty          []string
	}
	var have []got
	for _, c := range cs {
		have = append(have, got{c.Line, c.Text, c.Props, c.Assumes, c.empty})
	}
	want := []got{
		{3, "a paragraph's claim (prop: a), its citation on the next line (prop: m.b, c) and (assume: x). This line ends nothing; the blank one does.", []string{"a", "m.b", "c"}, []string{"x"}, nil},
		{8, "a list item (assume: y) continued.", nil, []string{"y"}, nil},
		{11, "numbered (prop: d)", []string{"d"}, nil, nil},
		{12, "quoted on two lines (prop: e)", []string{"e"}, nil, nil},
		{16, "no citation at all", nil, nil, nil},
		{18, "empty (prop: ) and (assume:)", nil, nil, []string{"prop", "assume"}},
	}
	if len(have) != len(want) {
		t.Fatalf("claims: %+v\nwant %+v", have, want)
	}
	for i := range want {
		h, w := have[i], want[i]
		if h.Line != w.Line || h.Text != w.Text || !slices.Equal(h.Props, w.Props) || !slices.Equal(h.Assumes, w.Assumes) || !slices.Equal(h.Empty, w.Empty) {
			t.Errorf("claim %d: %+v\nwant %+v", i, h, w)
		}
	}
	if s := cs[0].short(); s != "a paragraph's claim, its citation on the next line and. This..." {
		t.Errorf("short: %q", s)
	}
}

// Claim (model): marks a claim about the model alone, in each of the
// marker's forms; another parenthesis is kept for CheckClaims to refuse.
func TestClaimsModelOnly(t *testing.T) {
	src := "Claim (model): a (prop: a)\n\n" +
		"- **Claim (model):** b (prop: b)\n" +
		"- **Claim (model)**: c (prop: c)\n" +
		"- Claim: d (prop: d)\n" +
		"- Claim (code): e (prop: e)\n" +
		"- Claim (): f (prop: f)\n" +
		"- the Claim (model): mid-line is prose (prop: g)\n"
	var got []string
	for _, c := range Claims([]byte(src), "d.md") {
		got = append(got, fmt.Sprintf("%d %s %v %q", c.Line, c.Text, c.Model, c.scope))
	}
	want := []string{
		`1 a (prop: a) true ""`,
		`3 b (prop: b) true ""`,
		`4 c (prop: c) true ""`,
		`5 d (prop: d) false ""`,
		`6 e (prop: e) false "(code)"`,
		`7 f (prop: f) false "()"`,
	}
	if !slices.Equal(got, want) {
		t.Errorf("claims\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// CheckClaims: a citation qualified by a module the spec does not have is
// another model's, counted and left out, and a claim citing only such is
// left to those models' runs; a name the model lacks is an error ending
// with dropping the marker (not so a name of the other kind); a
// model-only claim's unbacked citations are notes of their own; a
// property a selection left out names that selection; and the fixes
// qualify their names when the code does.
func TestCheckClaims(t *testing.T) {
	m, _ := assumed(t, "m")
	if err := m.Exclude([]string{"settles"}); err != nil {
		t.Fatal(err)
	}
	table, errs := m.Table([]Annotation{
		{Kind: Enforces, Line: 1, Site: Site{Path: "a.go", Line: 2, Func: "A"}, Names: []string{"m.own"}},
		{Kind: Checks, Line: 3, Site: Site{Path: "a_test.go", Line: 4, Func: "TestA"}, Names: []string{"own"}},
	})
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	doc := "Claim: a (prop: positive) and (assume: oneWriter).\n\n" + // 1
		"Claim (model): b (prop: positive).\n\n" + // 3
		"**Claim (model):** c (prop: other.revocationBounded).\n\n" + // 5
		"Claim: d (prop: publication.x) (assume: publication.y).\n\n" + // 7
		"Claim: e (prop: own, publication.x).\n\n" + // 9
		"Claim: f (prop: gone).\n\n" + // 11
		"Claim (model): g (assume: gon).\n\n" + // 13
		"Claim (code): h (prop: positive).\n\n" + // 15
		"Claim: i (assume: positive).\n\n" + // 17
		"Claim: j (prop: settles).\n" // 19
	cc := table.CheckClaims(Claims([]byte(doc), "d.md"))
	if cc.Claims != 10 || cc.Elsewhere != 2 || cc.ModelOnly != 2 || cc.Props != 3 || cc.Assumes != 1 || !maps.Equal(cc.Others, map[string]int{"other": 1, "publication": 3}) {
		t.Errorf("counts: %+v", cc)
	}
	wantErrs := []string{
		`d.md:11: claim "f." cites property gone: model m has no property gone: cite one of own, positive, settles, or state the claim as a val in the model; or, if the document no longer makes this claim, drop Claim:`,
		`d.md:13: claim "g." cites assumption gon: model m has no assumption gon: cite one of model m's assumptions (small, oneWriter, tight), or declare it in the model (// @assume gon: what the model leaves out); or, if the document no longer makes this claim, drop Claim (model):`,
		`d.md:15: claim "h.": Claim (code): is no marker: write Claim: for a claim the code backs, or Claim (model): for one about the model alone`,
		`d.md:17: claim "i." cites assumption positive: positive is a property of model m, not an assumption: cite it as (prop: positive)`,
	}
	if !slices.Equal(cc.Errors, wantErrs) {
		t.Errorf("errors\n%s\nwant\n%s", strings.Join(cc.Errors, "\n"), strings.Join(wantErrs, "\n"))
	}
	wantUnbacked := []string{
		`d.md:1: claim "a and." cites property positive, which nothing enforces and no test checks: put //quint:enforces m.positive on the code that does, and //quint:checks m.positive on the test`,
		`d.md:1: claim "a and." cites assumption oneWriter, which nothing targets: put //quint:exercises m.oneWriter on a test or double that goes past it, or //quint:monitors m.oneWriter on the production check that watches it`,
		`d.md:19: claim "j." cites property settles, which this table leaves out (-exclude names it), so no code can be shown to back it: add it to the selection, or mark the claim Claim (model): if it is about the model alone`,
	}
	if !slices.Equal(cc.Unbacked, wantUnbacked) {
		t.Errorf("unbacked\n%s\nwant\n%s", strings.Join(cc.Unbacked, "\n"), strings.Join(wantUnbacked, "\n"))
	}
	if want := []string{`d.md:3: claim "b." cites property positive, which nothing enforces and no test checks: put //quint:enforces m.positive on the code that does, and //quint:checks m.positive on the test`}; !slices.Equal(cc.ModelNotes, want) {
		t.Errorf("model notes %q", cc.ModelNotes)
	}
}
