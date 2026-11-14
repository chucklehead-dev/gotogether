package connect

import (
	"strings"
	"testing"
)

func TestRequireCountsReachedStates(t *testing.T) {
	needQuint(t)
	out, err := connectProcess(t, "require", "^TestConnectProcessHelper$")
	if err != nil {
		t.Fatalf("a run reaching its required states failed: %v\n%s", err, out)
	}
	for _, want := range []string{"required nonNegative: reached in 10 of 10 traces (160 of 160 states)", "required bobSaves: reached in "} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestRequireFailsWhenNeverReached(t *testing.T) {
	needQuint(t)
	out := connectFailureOutput(t, "require-missed")
	for _, want := range []string{"required bobSaves: reached in 0 of 10 traces", "never reached the required bobSaves in 10 traces (seed 0x1)", "Raise MaxSamples or MaxSteps"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestRequireRefusesWhatIsNoBooleanVal(t *testing.T) {
	needQuint(t)
	for mode, want := range map[string]string{
		"require-unknown": "Config.Require names bobSpends, which is no val of testdata/bank.qnt: name a boolean val of the model",
		"require-int":     "Config.Require names MAX, which is 5 in state 0, not a boolean: name a boolean val of the model",
	} {
		if out := connectFailureOutput(t, mode); !strings.Contains(out, want) {
			t.Errorf("%s: missing %q in:\n%s", mode, want, out)
		}
	}
}
