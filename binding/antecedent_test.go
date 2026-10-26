package binding

import (
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
)

// An invariant `A implies B` has antecedent A, printed back as Quint;
// one of another form has none.
func TestAntecedents(t *testing.T) {
	skip.Quint(t)
	for _, c := range []struct{ spec, module, val, want string }{
		{"../examples/checkout/model/checkout.qnt", "checkout", "settledCorrectly", "settled"},
		{"../examples/hello/model/hello.qnt", "hello", "exactlyOnce", ""},
	} {
		m, err := LoadModel("quint", c.spec, c.module)
		if err != nil {
			t.Fatal(err)
		}
		if got := m.Antecedents[c.val]; got != c.want {
			t.Errorf("%s: antecedent %q, want %q", c.val, got, c.want)
		}
	}
}
