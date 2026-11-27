package invtable

import (
	"slices"
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
