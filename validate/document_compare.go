package validate

import (
	"fmt"
	"strings"
)

// DocComparison is a comparison of finite observation traces of two model
// instances. Every model transition contributes one observation; the comparison
// does not hide stuttering or establish fairness, liveness or refinement.
type DocComparison struct {
	Scope string `json:"scope"`
	// Basis identifies the complete Go graph/product analysis, not native equivalence proof.
	Basis string `json:"basis"`
	// Status is equivalent, different or inconclusive. Equivalent
	// applies only to the explicit instances and observation projection.
	Status string `json:"status"`
	// Observe names the model variables retained at every state, including init.
	Observe []string `json:"observe"`
	// PairLimit bounds each directional product search, not model exploration.
	PairLimit int `json:"pairLimit"`
	// WorkLimit bounds target initial-state and successor candidates examined per direction.
	WorkLimit int                `json:"workLimit"`
	Before    DocComparisonGraph `json:"before"`
	After     DocComparisonGraph `json:"after"`
	// Added is behavior admitted after but not before; Removed is the reverse.
	Added   DocComparisonDirection `json:"added"`
	Removed DocComparisonDirection `json:"removed"`
}

// DocComparisonGraph is an input instance's actual graph extent and search caps.
// Incomplete exploration cannot establish trace inclusion or equivalence.
type DocComparisonGraph struct {
	Binding string `json:"binding,omitempty"`
	Spec    string `json:"spec,omitempty"`
	Module  string `json:"module,omitempty"`
	// Constants are the effective configured Quint expressions for the compared
	// finite instance, not a claim that arbitrary expressions were reduced.
	Constants   []NamedValue  `json:"constants,omitempty"`
	Complete    bool          `json:"complete"`
	Cut         string        `json:"cut,omitempty"`
	Stopped     string        `json:"stopped,omitempty"`
	States      int           `json:"states"`
	Starts      int           `json:"starts"`
	Transitions int           `json:"transitions"`
	Depth       int           `json:"depth"`
	Bound       DocGraphBound `json:"bound"`
}

// DocComparisonDirection is one trace-inclusion search. Included requires the
// complete product search. A witness is an actual source trace absent from the
// target's complete graph; bounded or inconclusive outcomes do not assert absence.
type DocComparisonDirection struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
	// Explored counts the product nodes examined, not observations or model states.
	Explored int `json:"explored"`
	// Work counts target candidates examined; Cut names a reached pairs or work cap.
	Work    int                   `json:"work"`
	Cut     string                `json:"cut,omitempty"`
	Witness *DocComparisonWitness `json:"witness,omitempty"`
}

// DocComparisonWitness separates admission evidence from exclusion evidence.
// Native confirms only replay in the source; exclusion rests on the complete
// target graph and is not proved by a failed nondeterministic native sample.
type DocComparisonWitness struct {
	// Observed is the exact projected state sequence as named Quint literals.
	Observed [][]NamedValue `json:"observed"`
	// Source is the concrete source-model path, including full states and choices.
	Source *DocComparisonPath `json:"source,omitempty"`
	// Native is not_run, confirmed or unconfirmed, independently of ExclusionBasis.
	Native       string `json:"native"`
	NativeReason string `json:"nativeReason,omitempty"`
	// NativeAttempts counts source replay samples, NativeSeed the confirming seed.
	NativeAttempts int    `json:"nativeAttempts"`
	NativeSeed     string `json:"nativeSeed,omitempty"`
	ExclusionBasis string `json:"exclusionBasis"`
}

// DocComparisonPath is a concrete model path and its exact-state Quint replay.
// A replay's existence is not confirmation: DocComparisonWitness.Native records
// whether the generated replay ran successfully in Quint.
type DocComparisonPath struct {
	Steps  []DocComparisonStep `json:"steps"`
	Replay string              `json:"replay"`
	// Sampled lists transitions whose original call has multiple successors.
	Sampled []int `json:"sampled,omitempty"`
}

// DocComparisonStep is a source-model transition, or init, with exact values.
type DocComparisonStep struct {
	Call  string       `json:"call"`
	Picks []NamedValue `json:"picks,omitempty"`
	State []NamedValue `json:"state"`
}

// comparisonDetail describes typed directional evidence for CI renderings.
func comparisonDetail(name string, direction DocComparisonDirection) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %s", name, direction.Status)
	if direction.Reason != "" {
		fmt.Fprintf(&b, ": %s", direction.Reason)
	}
	if w := direction.Witness; w != nil {
		fmt.Fprintf(&b, "\nsource native replay: %s; exclusion basis: %s", w.Native, w.ExclusionBasis)
		if w.NativeReason != "" {
			fmt.Fprintf(&b, " (%s)", w.NativeReason)
		}
		for i, state := range w.Observed {
			fmt.Fprintf(&b, "\nobservation %d:", i)
			for _, v := range state {
				fmt.Fprintf(&b, " %s = %s", v.Name, v.Value)
			}
		}
		if w.Source != nil && w.Source.Replay != "" {
			fmt.Fprintf(&b, "\nsource replay: %s", w.Source.Replay)
		}
	}
	return b.String()
}

func comparisonConfirmed(direction DocComparisonDirection) bool {
	return direction.Status == "witness" && direction.Witness != nil && direction.Witness.Native == "confirmed"
}
