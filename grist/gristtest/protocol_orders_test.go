package gristtest_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/gristtest"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

func orderLabels(steps []qtrace.Step) string {
	var labels []string
	for _, s := range steps {
		if !s.Settled {
			labels = append(labels, fmt.Sprintf("%s/%s", s.Obs["protocol.service"], s.Obs["protocol.seq"]))
		}
	}
	return strings.Join(labels, ",")
}

// Two commands per store, optionally joined by peer causes and an intervening
// processor commit. The oracle below knows the constraints independently.
func orderFixture(mode string) ([]qtrace.Step, []*grist.App, []string, [][2]string) {
	shop, bank, sample := protocolPair()
	var steps []qtrace.Step
	labels := []string{"bank/1", "bank/2", "shop/1", "shop/2"}
	edges := [][2]string{{"bank/1", "bank/2"}, {"shop/1", "shop/2"}}
	for i := 1; i <= 2; i++ {
		for j, app := range []string{"shop", "bank"} {
			s := sample[j]
			s.Seq = uint64(i)
			s.Args = map[string]any{"id": fmt.Sprintf("%s%d", app, i)}
			s.Obs = map[string]any{"head": int64(i), "events": `["event"]`}
			if mode == "barrier" && app == "shop" && i == 2 {
				s.Seq = 3
				s.Obs["head"] = int64(3)
				labels[3] = "shop/3"
				edges[1][1] = "shop/3"
			}
			if mode == "normal" || mode == "reverse" || mode == "barrier" {
				trigger := i
				if mode == "reverse" {
					trigger = 3 - i
				}
				if app == "bank" {
					key := fmt.Sprintf("key%d", i)
					s.Args["id"] = key
					call := sample[2]
					call.Seq = uint64(i)
					call.Args = map[string]any{"key": key, "trigger": int64(trigger)}
					steps = append(steps, call)
					parent := trigger
					if mode == "barrier" {
						parent = 1
					} // event 2 belongs to the intervening processor
					edges = append(edges, [2]string{fmt.Sprintf("shop/%d", parent), fmt.Sprintf("bank/%d", i)})
				}
			}
			steps = append(steps, s)
		}
	}
	if mode == "barrier" {
		s := sample[0]
		s.Action = "charge"
		s.Seq = 2
		s.Args = map[string]any{}
		s.Obs = map[string]any{"head": int64(2), "events": `["event"]`}
		steps = append(steps, s)
	}
	end := sample[3]
	end.Seq = 1
	if mode != "independent" {
		end.Seq = 3
	}
	steps = append(steps, end)
	return steps, []*grist.App{shop, bank}, labels, edges
}

func bruteOrders(labels []string, edges [][2]string) map[string]bool {
	out := map[string]bool{}
	var permute func([]string, []string)
	permute = func(prefix, rest []string) {
		if len(rest) == 0 {
			for _, e := range edges {
				if slices.Index(prefix, e[0]) >= slices.Index(prefix, e[1]) {
					return
				}
			}
			out[strings.Join(prefix, ",")] = true
			return
		}
		for i, v := range rest {
			permute(append(slices.Clone(prefix), v), append(slices.Clone(rest[:i]), rest[i+1:]...))
		}
	}
	permute(nil, labels)
	return out
}

func TestProtocolOrdersAgainstBruteForce(t *testing.T) {
	for _, mode := range []string{"independent", "normal", "reverse", "barrier"} {
		t.Run(mode, func(t *testing.T) {
			steps, apps, labels, edges := orderFixture(mode)
			want := bruteOrders(labels, edges)
			canonical, err := gristtest.ProtocolSteps(steps, apps...)
			if err != nil {
				t.Fatal(err)
			}
			for _, reverse := range []bool{false, true} {
				input := slices.Clone(steps)
				if reverse {
					slices.Reverse(input)
				}
				seen := map[string]bool{}
				var first string
				result, err := gristtest.CheckProtocolOrders(t.Context(), input, len(want), func(_ context.Context, order []qtrace.Step) (bool, error) {
					key := orderLabels(order)
					if seen[key] {
						t.Fatalf("duplicate projection: %s", key)
					}
					seen[key] = true
					if first == "" {
						first = key
					}
					if len(order) != 5 || !order[4].Settled {
						t.Fatalf("want four commands and settle: %v", order)
					}
					for i, s := range order {
						if s.Seq != uint64(i+1) {
							t.Fatalf("non-dense sequence: %v", order)
						}
					}
					return true, nil
				}, apps...)
				if err != nil || !result.Exhaustive || result.Checked != len(want) || result.Counterexample != nil || !reflect.DeepEqual(seen, want) || first != orderLabels(canonical) {
					t.Fatalf("reverse=%v: got %+v, %v, orders %v; want %v, first %s", reverse, result, err, seen, want, orderLabels(canonical))
				}
			}
		})
	}
}

func TestProtocolOrdersLimitsAndFailure(t *testing.T) {
	steps, apps, _, _ := orderFixture("independent")
	for _, limit := range []int{1, 5, 6, 7} {
		result, err := gristtest.CheckProtocolOrders(t.Context(), steps, limit, func(context.Context, []qtrace.Step) (bool, error) { return true, nil }, apps...)
		if err != nil || result.Checked != min(limit, 6) || result.Exhaustive != (limit >= 6) || result.Counterexample != nil {
			t.Fatalf("limit %d: %+v, %v", limit, result, err)
		}
	}
	var retained [][]qtrace.Step
	result, err := gristtest.CheckProtocolOrders(t.Context(), steps, 6, func(_ context.Context, order []qtrace.Step) (bool, error) {
		retained = append(retained, order)
		return len(retained) < 2, nil
	}, apps...)
	if err != nil || result.Exhaustive || result.Checked != 2 || !reflect.DeepEqual(result.Counterexample, retained[1]) || orderLabels(retained[0]) == orderLabels(retained[1]) {
		t.Fatalf("counterexample: %+v, %v", result, err)
	}
	var buf bytes.Buffer
	if err := qtrace.WriteJSONL(&buf, result.Counterexample); err != nil {
		t.Fatal(err)
	}
	roundTrip, err := qtrace.ReadJSONL(&buf)
	if err != nil || orderLabels(roundTrip) != orderLabels(result.Counterexample) {
		t.Fatalf("identity roundtrip: %v, %v", roundTrip, err)
	}
	// A retained candidate must not be overwritten by a later one.
	retained[1][0].Obs["protocol.seq"] = "changed"
	if retained[0][0].Obs["protocol.seq"] == "changed" || steps[0].Obs["protocol.seq"] != nil {
		t.Fatal("candidate maps alias another candidate or native input")
	}
}

func TestProtocolOrdersErrorsAndCancellation(t *testing.T) {
	steps, apps, _, _ := orderFixture("independent")
	toolErr := errors.New("evaluator unavailable")
	calls := 0
	result, err := gristtest.CheckProtocolOrders(t.Context(), steps, 6, func(context.Context, []qtrace.Step) (bool, error) { calls++; return true, toolErr }, apps...)
	if !errors.Is(err, toolErr) || calls != 1 || result.Checked != 0 || result.Exhaustive || result.Counterexample != nil {
		t.Fatalf("tool failure became verdict: %+v, %v", result, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	result, err = gristtest.CheckProtocolOrders(ctx, steps, 6, func(context.Context, []qtrace.Step) (bool, error) { cancel(); return true, nil }, apps...)
	if !errors.Is(err, context.Canceled) || result.Checked != 1 || result.Exhaustive {
		t.Fatalf("canceled: %+v, %v", result, err)
	}
	result, err = gristtest.CheckProtocolOrders(ctx, steps, 6, func(context.Context, []qtrace.Step) (bool, error) {
		t.Fatal("called after cancellation")
		return true, nil
	}, apps...)
	if !errors.Is(err, context.Canceled) || result.Checked != 0 {
		t.Fatalf("pre-canceled: %+v, %v", result, err)
	}
	for _, limit := range []int{-1, 0} {
		_, err := gristtest.CheckProtocolOrders(t.Context(), steps, limit, func(context.Context, []qtrace.Step) (bool, error) { t.Fatal("invalid limit checked"); return true, nil }, apps...)
		if err == nil {
			t.Fatal("invalid limit accepted")
		}
	}
	if _, err := gristtest.CheckProtocolOrders(t.Context(), steps, 1, nil, apps...); err == nil {
		t.Fatal("nil check accepted")
	}
	steps[0].Seq = 9
	if _, err := gristtest.CheckProtocolOrders(t.Context(), steps, 6, func(context.Context, []qtrace.Step) (bool, error) { t.Fatal("bad evidence checked"); return true, nil }, apps...); err == nil {
		t.Fatal("bad evidence accepted")
	}
}

func TestProtocolOrdersTenantsAndPrefixes(t *testing.T) {
	steps, apps, _, _ := orderFixture("independent")
	// Two independent model instances each have six orders, not 36, nor
	// all possible cross-tenant shuffles. Global settle covers both.
	var other []qtrace.Step
	for _, s := range steps[:len(steps)-1] {
		s.Actor = strings.ReplaceAll(s.Actor, "/t1", "/t2")
		s.Recorder = strings.ReplaceAll(s.Recorder, "/t1", "/t2")
		other = append(other, s)
	}
	steps = append(other, steps...)
	for _, limit := range []int{6, 11, 12} {
		result, err := gristtest.CheckProtocolOrders(t.Context(), steps, limit, func(_ context.Context, order []qtrace.Step) (bool, error) {
			for _, s := range order {
				if s.Actor != order[0].Actor {
					t.Fatal("tenants mixed")
				}
			}
			return true, nil
		}, apps...)
		if err != nil || result.Checked != limit || result.Exhaustive != (limit == 12) {
			t.Fatalf("tenant limit %d: %+v, %v", limit, result, err)
		}
	}
	result, err := gristtest.CheckProtocolOrders(t.Context(), steps[:len(steps)-1], 12, func(_ context.Context, order []qtrace.Step) (bool, error) {
		for _, s := range order {
			if s.Settled {
				t.Fatal("invented settle")
			}
		}
		return true, nil
	}, apps...)
	if err != nil || !result.Exhaustive {
		t.Fatalf("prefix: %+v, %v", result, err)
	}
}
