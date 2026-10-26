package qenv

import (
	"fmt"
	"slices"

	"github.com/chucklehead-dev/gotogether/qenv/internal/choice"
)

// A FaultKind is a kind of fault and its options' names, nominal (option
// 0) left unnamed: what a fault choice means, not just how many options it
// has. Declare it once (Faults), and make its choices with Sim.FaultOf:
// coverage and scripts then name options ("lostResponse", not 2).
type FaultKind struct {
	Name    string
	Options []string
	owner   *Process // the double's process: a core settle faults it if outside the core
}

// at is the kind of a double in process p (SettleCore).
func (k FaultKind) at(p *Process) FaultKind { k.owner = p; return k }

// At is the kind of a double that lives in process p, for a double built
// outside qenv (qenv/qhttp's server): a core settle (Sim.SettleCore) goes
// on faulting it if p is outside the core, as it does qenv's own doubles.
func (k FaultKind) At(p *Process) FaultKind { return k.at(p) }

// DeclareFaults declares a fault kind: name without the "fault." prefix ("kube.request")
// and its options in order (choice.Declare: the one registry, which also
// holds the sites DeclareFault registers). Declaring it again with other
// options panics: option numbers would change meaning.
func DeclareFaults(name string, options ...string) FaultKind {
	choice.Declare(name, options...)
	return FaultKind{Name: name, Options: append([]string(nil), options...)}
}

// Only is the kind restricted to its first options, named, for a call that
// cannot fail in the others (a read cannot lose an update's response).
// They must be a prefix of the kind's options, so that an option's number
// means the same option at every call; anything else panics.
func (k FaultKind) Only(options ...string) FaultKind {
	if len(options) > len(k.Options) || !slices.Equal(k.Options[:len(options)], options) {
		panic(fmt.Sprintf("qenv: fault kind %s: Only(%v) is not a prefix of its options %v", k.Name, options, k.Options))
	}
	return FaultKind{Name: k.Name, Options: k.Options[:len(options)], owner: k.owner}
}

// FaultOf makes a fault choice of the kind: "" for nominal, else the
// option's name.
func (s *Sim) FaultOf(k FaultKind) string {
	v := s.faultAt(k.owner, k.Name, 1+len(k.Options))
	if v == 0 {
		return ""
	}
	return k.Options[v-1]
}
