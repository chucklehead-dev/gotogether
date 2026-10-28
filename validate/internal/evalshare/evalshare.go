// Package evalshare is what validate/model reaches of validate's Go
// evaluator: the compiled evaluation module of a binding's model, built
// and cached by validate (EvaluatorOf, set when validate initializes,
// which every importer of validate/model does), and the helpers both read
// transitions with. Internal: neither package's API.
package evalshare

import (
	"strings"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/internal/qeval"
)

// An Eval is a binding's model compiled for the Go evaluator: its
// evaluation module and the IR quint compiled it to, or why it has none
// (Err), and its reader of Quint literals as the model's values.
type Eval struct {
	Module  *binding.EvalModule
	Model   *qeval.Model
	Err     error
	Literal func(s string) (qeval.Value, error)
}

// EvaluatorOf is validate's evaluator of checker (a *validate.Checker)
// for the evaluation module make writes, compiled once by its text and
// the model's source. Set by validate's init. Process-wide.
var EvaluatorOf func(checker any, make func(path string) (*binding.EvalModule, error)) *Eval

// ShortName is a model variable's name without its module's qualifier.
func ShortName(v string) string { return v[strings.LastIndex(v, ":")+1:] }

// BranchEvent is the action of t that the model's step branched to:
// its first event that is neither step nor a helper.
func BranchEvent(m *binding.Model, t qeval.Transition) (qeval.Event, bool) {
	for _, a := range t.Actions() {
		if a.Action == "step" || m.Helpers[a.Action] {
			continue
		}
		return a, true
	}
	return qeval.Event{}, false
}
