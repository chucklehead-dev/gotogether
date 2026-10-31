package validate

import (
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/qeval"
)

func TestChoiceStateLiteral(t *testing.T) {
	values := []qeval.Value{
		qeval.Int(3), qeval.Bool(true), qeval.Str("quoted\"\n"),
		qeval.Tup{qeval.Int(1), qeval.Str("s")},
		qeval.List{qeval.Bool(false)},
		&qeval.Set{Elems: []qeval.Value{qeval.Int(1)}},
		&qeval.Map{Keys: []qeval.Value{qeval.Int(1)}, Vals: []qeval.Value{qeval.Str("one")}},
		&qeval.Rec{Names: []string{"field"}, Vals: []qeval.Value{qeval.List{qeval.Int(2)}}},
	}
	for _, value := range values {
		got, err := choiceStateLiteral(value)
		if err != nil || got != qeval.String(value) {
			t.Errorf("%T: got %q, %v", value, got, err)
		}
	}
	variant := qeval.Variant{Label: "MissingConstructor", Val: qeval.Tup{}}
	bad := []qeval.Value{nil, variant, &qeval.Lambda{},
		qeval.List{variant}, qeval.Tup{variant}, &qeval.Set{Elems: []qeval.Value{variant}},
		&qeval.Map{Keys: []qeval.Value{variant}, Vals: []qeval.Value{qeval.Int(0)}},
		&qeval.Map{Keys: []qeval.Value{qeval.Int(0)}, Vals: []qeval.Value{variant}},
		&qeval.Rec{Names: []string{"field"}, Vals: []qeval.Value{variant}},
		(*qeval.Set)(nil), (*qeval.Map)(nil), (*qeval.Rec)(nil),
		&qeval.Map{Keys: []qeval.Value{qeval.Int(0)}}, &qeval.Rec{Names: []string{"field"}},
	}
	for _, value := range bad {
		if got, err := choiceStateLiteral(value); err == nil || got != "" {
			t.Errorf("accepted %T: %q, %v", value, got, err)
		}
	}
	cycle := &qeval.Set{}
	cycle.Elems = []qeval.Value{cycle}
	if _, err := choiceStateLiteral(cycle); err == nil {
		t.Fatal("accepted cyclic literal")
	}
}
