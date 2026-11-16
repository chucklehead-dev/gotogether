package counter

import (
	"context"
	"testing"

	cn "github.com/chucklehead-dev/gotogether/connect"
)

// TestModel drives reset through a Switch, connect imported under
// another name.
func TestModel(t *testing.T) {
	c := &Counter{Cap: 3}
	cn.Test(t, cn.Config{Spec: "../model/counter.qnt"}, cn.Switch{
		"init": func(*cn.Step) error { *c = Counter{Cap: 3}; return nil },
		"reset": func(*cn.Step) error {
			c.Reset(context.Background())
			return nil
		},
	})
}
