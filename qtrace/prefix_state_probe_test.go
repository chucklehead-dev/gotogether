package qtrace

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// A longer rejected order can establish a prefix's acceptance, but its
// state belongs only to its exact order. State recovery must ask that prefix
// itself, at most once, and different states of the same placed calls stay apart.
func TestPrefixStateRecoveryIsExactAndCharged(t *testing.T) {
	steps := []Step{op("a", 1, "write", 1, 0, 10), op("b", 1, "write", 2, 0, 10), op("c", 1, "read", 9, 20, 30)}
	key := func(o []Step) string {
		var b strings.Builder
		for _, s := range o {
			fmt.Fprintf(&b, "%s:%v:%v;", s.Action, s.Args["v"], s.Obs["v"])
		}
		return b.String()
	}
	for _, mode := range []string{"exact", "nil", "unavailable", "intermittent"} {
		t.Run(mode, func(t *testing.T) {
			asked, inherited := map[string]bool{}, map[string]bool{}
			checks, recoveries := 0, 0
			states := map[string]string{}
			check := func(orders [][]Step) ([]int, []string, error) {
				fs, st := registerStates(orders)
				checks += len(orders)
				for i, o := range orders {
					k := key(o)
					if asked[k] {
						t.Errorf("exact order asked twice: %s", k)
					}
					if inherited[k] && !asked[k] {
						recoveries++
					}
					asked[k] = true
					if fs[i] < 0 {
						states[k] = st[i]
					} else {
						// A failed longer candidate's leftover state says nothing
						// about its accepted prefixes and must be ignored.
						st[i] = "longer-candidate-state-is-not-a-prefix-state"
					}
					upTo := len(o)
					if fs[i] >= 0 {
						upTo = fs[i]
					}
					for n := 1; n <= upTo; n++ {
						inherited[key(o[:n])] = true
					}
				}
				switch mode {
				case "nil":
					st = nil
				case "unavailable":
					st = make([]string, len(orders))
				case "intermittent":
					if checks%2 == 0 {
						st = nil
					}
				}
				return fs, st, nil
			}
			got, err := Linearize(steps, LinearizeOptions{Budget: 200}, check)
			if err != nil || got.Verdict != NotLinearizable || got.Proof == nil {
				t.Fatalf("read of 9 changed verdict: %+v, %v", got, err)
			}
			if checks > got.Tried || got.Tried > 400 {
				t.Fatalf("uncharged checker work: %d checks, %d charged candidates", checks, got.Tried)
			}
			if len(got.Proof.Merged) != 0 {
				t.Fatal("distinct last-write states or unavailable states were merged")
			}
			if mode == "exact" {
				if recoveries == 0 || states[key(steps[:1])] != "1" || states[key(steps[1:2])] != "2" {
					t.Fatalf("exact prefix states not recovered: %d, %v", recoveries, states)
				}
			} else if mode == "nil" && recoveries != 0 {
				t.Fatal("nil-state checker lost its verdict-only cache")
			}
		})
	}
}

// Two orders of the same calls reach overlapping but different possible-state
// sets. The shared sampled state (2) is not a future-equivalence key: only one
// order can explain the later read of 1.
func TestPrefixStateKeysPreserveNondeterministicFutures(t *testing.T) {
	steps := []Step{op("a", 1, "choose", 0, 0, 10), op("b", 1, "double", 0, 0, 10), op("c", 1, "read", 1, 20, 30)}
	for _, keysAvailable := range []bool{false, true} {
		t.Run(fmt.Sprint(keysAvailable), func(t *testing.T) {
			check := func(orders [][]Step) ([]int, []string) {
				fs, keys := make([]int, len(orders)), make([]string, len(orders))
				for i, order := range orders {
					fs[i] = -1
					possible := []int64{0}
					for j, s := range order {
						switch s.Action {
						case "choose":
							possible = []int64{1, 2}
						case "double":
							for k := range possible {
								possible[k] *= 2
							}
						case "read":
							want := s.Obs["v"].(int64)
							if !slices.Contains(possible, want) {
								fs[i] = j
							} else {
								possible = []int64{want}
							}
						}
						if fs[i] >= 0 {
							break
						}
					}
					if fs[i] < 0 && keysAvailable {
						keys[i] = fmt.Sprint(possible)
					}
				}
				return fs, keys
			}
			got, ok, _, cut := linearize(steps, 200, check, false, nil)
			if !ok || cut || len(got) != 3 || got[0].Action != "double" || got[1].Action != "choose" || !allowed(steps, got) {
				t.Fatalf("possible-state sets lost a conforming history: %v, ok %v cut %v", got, ok, cut)
			}
		})
	}
}
