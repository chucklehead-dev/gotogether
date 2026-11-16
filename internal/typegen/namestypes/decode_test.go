package namestypes

import (
	"reflect"
	"testing"

	"github.com/chucklehead-dev/gotogether/itf"
)

// The renamed names decode their own values: a field renamed for a
// collision (ab and Ab, _hidden and hidden) is filled by its tag, not by
// the other's; the variant named Tag fills Tag_, its tag the Tag field; a
// map keyed by an Option is a slice of pairs, one keyed by a record a Go
// map; lib's Item and the model's are each their own; MaybeItem, an
// application of Option, is *Item, and Option[str] beside it is *string;
// Shape, whose
// variants Decode cannot tell apart, is the value itself.
func TestRenamedNamesDecode(t *testing.T) {
	st := itf.RecordOf(
		"st", itf.RecordOf("n", 1),
		"shape", itf.VariantOf("Ab", itf.StrOf("x")),
		"cmd", itf.VariantOf("Tag", itf.IntOf(5)),
		"rec", itf.RecordOf("ab", 1, "Ab", 2, "_hidden", 3, "hidden", "h", "func", true, "go", 4),
		"li", itf.RecordOf("a", 6),
		"it", itf.RecordOf("b", 7),
		"mode", itf.Enum("Fast"),
		"fast", itf.RecordOf("x", 8),
		"byOpt", itf.Value{Kind: itf.Map, Pairs: [][2]itf.Value{{itf.None(), itf.StrOf("none")}, {itf.VariantOf("Some", itf.IntOf(2)), itf.StrOf("two")}}},
		"byPoint", itf.Value{Kind: itf.Map, Pairs: [][2]itf.Value{{itf.RecordOf("x", 1, "y", 2), itf.IntOf(3)}}},
		"maybe", itf.VariantOf("Some", itf.RecordOf("b", 9)),
		"note", itf.VariantOf("Some", itf.StrOf("n")),
	)
	var got State_
	if err := itf.DecodeProjection(st, &got); err != nil {
		t.Fatal(err)
	}
	two, n := 2, "n"
	want := State_{
		Maybe: &Item{B: 9},
		Note:  &n,
		St:    State{N: 1},
		Shape: itf.VariantOf("Ab", itf.StrOf("x")),
		Cmd:   Cmd{Tag: CmdTag_, Tag_: 5},
		Rec:   Fields{Ab: 1, Ab_: 2, Hidden: 3, Hidden_: "h", Func: true, Go: 4},
		Li:    Item_{A: 6},
		It:    Item{B: 7},
		Mode:  ModeFast_,
		Fast:  ModeFast{X: 8},
		ByOpt: []struct {
			Key   *int
			Value string
		}{{nil, "none"}, {&two, "two"}},
		ByPoint: map[Point]int{{X: 1, Y: 2}: 3},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("decoded\n%+v\nwant\n%+v", got, want)
	}
	var run Cmd
	if err := itf.Decode(itf.Enum("Run"), &run); err != nil || run != (Cmd{Tag: CmdRun}) {
		t.Errorf("Run decoded as %+v, %v", run, err)
	}
}
