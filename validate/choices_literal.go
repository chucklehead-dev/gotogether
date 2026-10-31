package validate

import (
	"fmt"

	"github.com/chucklehead-dev/gotogether/internal/qeval"
)

// choiceStateLiteral admits only values whose printed form is self-contained
// Quint source. Constructors and operators need source bindings unavailable in
// the generated replay module and must not become a failed confirmation run.
func choiceStateLiteral(value qeval.Value) (string, error) {
	var check func(qeval.Value, int) error
	check = func(v qeval.Value, depth int) error {
		if depth > 256 {
			return fmt.Errorf("choice state literal nesting exceeds 256 levels")
		}
		var children []qeval.Value
		switch x := v.(type) {
		case qeval.Int, qeval.Bool, qeval.Str:
			return nil
		case qeval.Tup:
			children = x
		case qeval.List:
			children = x
		case *qeval.Set:
			if x == nil {
				return fmt.Errorf("nil choice state set")
			}
			children = x.Elems
		case *qeval.Map:
			if x == nil || len(x.Keys) != len(x.Vals) {
				return fmt.Errorf("malformed choice state map")
			}
			for _, key := range x.Keys {
				if err := check(key, depth+1); err != nil {
					return err
				}
			}
			children = x.Vals
		case *qeval.Rec:
			if x == nil || len(x.Names) != len(x.Vals) {
				return fmt.Errorf("malformed choice state record")
			}
			children = x.Vals
		default:
			return fmt.Errorf("unsupported choice state literal %T", v)
		}
		for _, child := range children {
			if err := check(child, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if err := check(value, 0); err != nil {
		return "", err
	}
	return qeval.String(value), nil
}
