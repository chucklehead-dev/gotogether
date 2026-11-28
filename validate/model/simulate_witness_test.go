package model

import (
	"strings"
	"testing"
)

// readWitnesses reads the counts both admitted quint lines write: 0.33's
// "witnessed" under Witnesses:, and 0.34's "observed" under
// Observations: (--witnesses deprecated, its predicates counted as
// observations), each taken from `quint run --witnesses` of that version.
// A neighbour's name (_1 and _10) is not read for another's.
func TestReadWitnessesAcrossQuintVersions(t *testing.T) {
	for name, out := range map[string]string{
		"0.33.1": "[ok] No violation found (10ms).\nWitnesses:\n__gtg_possible_10 was witnessed in 4 trace(s) out of 5 explored (80.00%)\n__gtg_possible_1 was witnessed in 2 trace(s) out of 5 explored (40.00%)\n__gtg_possible_2 was witnessed in 0 trace(s) out of 5 explored (0.00%)\n",
		"0.35.0": "Warning: --witnesses is deprecated. Declare observation definitions in the spec, or use --observation to stop at the first trace that reaches one.\n[ok] No violation found (22ms).\nObservations:\n__gtg_possible_10 was observed in 4 trace(s) out of 5 explored (80.00%)\n__gtg_possible_1 was observed in 2 trace(s) out of 5 explored (40.00%)\n__gtg_possible_2 was observed in 0 trace(s) out of 5 explored (0.00%)\n",
	} {
		t.Run(name, func(t *testing.T) {
			one, two := &possibleCheck{val: "__gtg_possible_1"}, &possibleCheck{val: "__gtg_possible_2"}
			if err := readWitnesses(out, []*possibleCheck{one, two}); err != nil {
				t.Fatal(err)
			}
			if !one.quintFound || one.quintWitnessN != 2 || two.quintFound || two.quintWitnessN != 0 {
				t.Errorf("read %v/%d and %v/%d, want true/2 and false/0", one.quintFound, one.quintWitnessN, two.quintFound, two.quintWitnessN)
			}
		})
	}
	missing := &possibleCheck{val: "__gtg_possible_3"}
	if err := readWitnesses("Observations:\n__gtg_possible_1 was observed in 2 trace(s) out of 5 explored (40.00%)\n", []*possibleCheck{missing}); err == nil || !strings.Contains(err.Error(), "names no count of witness __gtg_possible_3") {
		t.Errorf("a predicate quint does not count read as %v, want the missing-count error", err)
	}
}
