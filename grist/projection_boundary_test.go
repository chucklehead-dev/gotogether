package grist_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/simstore"
	"github.com/chucklehead-dev/gotogether/grist/store"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
)

type projectionNumber struct {
	Value uint64 `json:"value"`
}
type projectionArgs struct{}

// The values originate inside application code, so command-argument validation
// cannot guard these events. A bad event must never gain a success marker.
func TestProjectionCommandIntegerBoundary(t *testing.T) {
	for _, n := range []uint64{math.MaxInt64, math.MaxInt64 + 1, math.MaxUint64} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			a := grist.New("projection")
			number := grist.NewEvent[projectionNumber](a, "number")
			cmd := grist.NewCommand(a, "add", func(*grist.Reads, projectionArgs) ([]grist.Event, error) {
				return []grist.Event{number.Of(projectionNumber{n})}, nil
			}, grist.Appends(number))
			if err := a.Verify(); err != nil {
				t.Fatal(err)
			}
			s := newStoreNamed(t, a.Name)
			ctx := context.Background()
			rc, err := cmd.Execute(ctx, s, grist.Envelope{ID: "number", Tenant: "t1"}, projectionArgs{})
			_, marked, merr := s.Marker(ctx, "t1", "number")
			if merr != nil {
				t.Fatal(merr)
			}
			_, head, rerr := s.Read(ctx, "t1", grist.Selector{Types: []string{"number"}}, 0)
			if rerr != nil {
				t.Fatal(rerr)
			}
			if n > math.MaxInt64 {
				var rejection *grist.Rejected
				if err == nil || errors.Is(err, grist.ErrBadArgs) || errors.As(err, &rejection) {
					t.Errorf("unrepresentable emitted event must be an application error, got %v", err)
				}
				if marked || head != 0 || rc.Pos != 0 {
					t.Errorf("invalid event committed: marker=%v head=%d receipt=%+v", marked, head, rc)
				}
				return
			}
			if err != nil || !marked || head != 1 {
				t.Fatalf("valid boundary: receipt=%+v marker=%v head=%d error=%v", rc, marked, head, err)
			}
			sink := &qobs.MemorySink{}
			relay, e := grist.NewRelay(s)
			if e != nil {
				t.Fatal(e)
			}
			relay.RenderAs(a)
			if e = relay.Flush(qobs.WithRecorder(ctx, qobs.NewRecorder(sink))); e != nil {
				t.Fatal(e)
			}
			steps := sink.Steps()
			if len(steps) != 1 || steps[0].Obs["ints"] != fmt.Sprintf("[%d]", n) {
				t.Fatalf("exact boundary observation: %v", steps)
			}
		})
	}
}

func projectionRow(t *testing.T, observed string, rendered int) []byte {
	t.Helper()
	kv := [][]any{{"obs.events", []string{"number"}}, {"obs.strs", []string{}}, {"obs.ints", json.RawMessage(observed)}, {"obs.causes", [][]string{{"command", "one"}}}}
	if rendered > 0 {
		kv = append(kv, []any{"grist.rendered", []int{rendered}})
	}
	b, err := json.Marshal(map[string]any{"v": 1, "steps": []any{map[string]any{"action": "add", "events": 1, "kv": kv}}})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Bodies are deliberately written below the App boundary to model retained
// evidence. A rendered-version hint cannot authorize trusting corrupt data.
func TestProjectionRelayRejectsInvalidCurrentBodies(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"overflow", `{"value":18446744073709551615}`},
		{"wrong field type", `{"value":"wrong"}`},
		{"null", `null`},
	} {
		for _, rendered := range []int{0, 1} {
			t.Run(fmt.Sprintf("%s/rendered=%d", tc.name, rendered), func(t *testing.T) {
				a := grist.New("projection")
				grist.NewEvent[projectionNumber](a, "number")
				if err := a.Verify(); err != nil {
					t.Fatal(err)
				}
				s := newStoreNamed(t, a.Name)
				ctx := context.Background()
				_, err := s.Append(ctx, "t1", store.Batch{Events: []grist.Event{{Type: "number", Version: 1, Body: json.RawMessage(tc.body)}}, Cause: "one", Step: projectionRow(t, "[-1]", rendered)})
				if err != nil {
					t.Fatal(err)
				}
				sink := &qobs.MemorySink{}
				relay, err := grist.NewRelay(s)
				if err != nil {
					t.Fatal(err)
				}
				relay.RenderAs(a)
				err = relay.Flush(qobs.WithRecorder(ctx, qobs.NewRecorder(sink)))
				if err == nil {
					t.Error("invalid retained event relayed without a data error")
				}
				if len(sink.Steps()) != 0 {
					t.Errorf("invalid row produced observations: %v", sink.Steps())
				}
				behind, e := relay.Behind(ctx)
				if e != nil {
					t.Fatal(e)
				}
				if behind != 1 {
					t.Errorf("invalid row advanced relay: behind=%d", behind)
				}
			})
		}
	}
}

// A processor's declared success event is known before its effect runs. Invalid
// evidence is a handling failure, never a successful external side effect.
func TestProjectionProcessorIntegerBoundary(t *testing.T) {
	for _, kind := range []string{"emit", "at-least-once", "at-most-once"} {
		for _, n := range []uint64{math.MaxInt64, math.MaxInt64 + 1, math.MaxUint64} {
			t.Run(fmt.Sprintf("%s/%d", kind, n), func(t *testing.T) {
				a := grist.New("projection")
				added := grist.NewEvent[Added](a, "added")
				number := grist.NewEvent[projectionNumber](a, "number")
				effects := 0
				grist.NewProcessor(a, "project", added, func(context.Context, *grist.Reads, Added, grist.Meta) grist.Result {
					event := number.Of(projectionNumber{n})
					if kind == "emit" {
						return grist.Emit(event)
					}
					guarantee := grist.AtLeastOnce
					if kind == "at-most-once" {
						guarantee = grist.AtMostOnce
					}
					return grist.Effect(func(context.Context) error { effects++; return nil }, guarantee, grist.OnSuccess(event))
				})
				if err := a.Verify(); err != nil {
					t.Fatal(err)
				}
				sink := &qobs.MemorySink{}
				sim := qenv.New(qenv.WithContext(qenv.NewSeeded(1), qobs.WithRecorder(context.Background(), qobs.NewRecorder(sink))))
				defer sim.Close()
				sim.Check = true
				s := simstore.New(qenv.NewStore(sim, a.Name, qenv.Serializable))
				ctx := sim.Context()
				if err := s.CreateTenant(ctx, "t1"); err != nil {
					t.Fatal(err)
				}
				if _, err := s.Append(ctx, "t1", store.Batch{Events: []grist.Event{added.Of(Added{By: 1})}}); err != nil {
					t.Fatal(err)
				}
				sim.Process("node", true, func(ctx context.Context) {
					if err := a.Start(ctx, s); err != nil {
						t.Error(err)
					}
				})
				if err := sim.Run(time.Minute); err != nil {
					t.Fatal(err)
				}
				events, _, err := s.Read(ctx, "t1", grist.Selector{Types: []string{"number", "grist.failed"}}, 0)
				if err != nil {
					t.Fatal(err)
				}
				numbers, failed := 0, 0
				for _, e := range events {
					if e.Type == "number" {
						numbers++
					} else {
						failed++
					}
				}
				if n > math.MaxInt64 {
					if numbers != 0 || failed != 1 || effects != 0 {
						t.Errorf("invalid projection numbers=%d failures=%d effects=%d, want0/1/0", numbers, failed, effects)
					}
					diagnostic := false
					for _, step := range sink.Steps() {
						if strings.Contains(step.Error, "field value") && strings.Contains(step.Error, "int64") {
							diagnostic = true
						}
					}
					if !diagnostic {
						t.Errorf("processor projection error lost field/range diagnosis: %v", sink.Steps())
					}
				} else {
					wantEffects := 1
					if kind == "emit" {
						wantEffects = 0
					}
					if numbers != 1 || failed != 0 || effects != wantEffects {
						t.Errorf("valid projection numbers=%d failures=%d effects=%d", numbers, failed, effects)
					}
					observed := 0
					for _, step := range sink.Steps() {
						if step.Obs["events"] == `["number"]` {
							observed++
							if step.Obs["ints"] != fmt.Sprintf("[%d]", n) {
								t.Errorf("processor boundary observation: %v", step)
							}
						}
					}
					if observed != 1 {
						t.Errorf("number observations=%d, want 1", observed)
					}
				}
			})
		}
	}
}

type projectionOld struct {
	Value uint64 `json:"value"`
}
type projectionMiddle struct {
	Value uint64 `json:"value"`
}

func TestProjectionRelayVersionBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name    string
		version int
		value   uint64
		clamp   bool
		bad     bool
	}{
		{"valid old", 1, 6, false, false},
		{"invalid old source", 1, math.MaxUint64, true, true},
		{"invalid newest", 3, math.MaxUint64, false, true},
		{"invalid upcast result", 1, 7, false, true},
		{"invalid intermediate discarded later", 1, 7, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := grist.New("projection")
			grist.NewEvent[projectionNumber](a, "number",
				grist.Upcast(1, func(v projectionOld) projectionMiddle {
					if v.Value == 7 {
						return projectionMiddle{math.MaxUint64}
					}
					return projectionMiddle{v.Value}
				}),
				grist.Upcast(2, func(v projectionMiddle) projectionNumber {
					if tc.clamp && v.Value == math.MaxUint64 {
						return projectionNumber{0}
					}
					return projectionNumber{v.Value}
				}))
			if err := a.Verify(); err != nil {
				t.Fatal(err)
			}
			s := newStoreNamed(t, a.Name)
			ctx := context.Background()
			body, err := json.Marshal(projectionNumber{tc.value})
			if err != nil {
				t.Fatal(err)
			}
			observed := fmt.Sprintf("[%d]", tc.value)
			if tc.value > math.MaxInt64 {
				observed = "[-1]"
			}
			if _, err = s.Append(ctx, "t1", store.Batch{Events: []grist.Event{{Type: "number", Version: tc.version, Body: body}}, Cause: "one", Step: projectionRow(t, observed, tc.version)}); err != nil {
				t.Fatal(err)
			}
			sink := &qobs.MemorySink{}
			relay, err := grist.NewRelay(s)
			if err != nil {
				t.Fatal(err)
			}
			relay.RenderAs(a)
			err = relay.Flush(qobs.WithRecorder(ctx, qobs.NewRecorder(sink)))
			if tc.bad {
				if err == nil || len(sink.Steps()) != 0 {
					t.Errorf("invalid versioned data emitted: error=%v steps=%v", err, sink.Steps())
				}
				if behind, e := relay.Behind(ctx); e != nil || behind != 1 {
					t.Errorf("invalid row cursor: behind=%d error=%v", behind, e)
				}
			} else if err != nil || len(sink.Steps()) != 1 || sink.Steps()[0].Obs["ints"] != "[6]" {
				t.Errorf("valid old event: error=%v steps=%v", err, sink.Steps())
			}
		})
	}
}

// An old reader must not decode a newer writer's event using its own shape.
func TestProjectionRelayPreservesNewerWriter(t *testing.T) {
	a := grist.New("projection")
	grist.NewEvent[projectionNumber](a, "number")
	if err := a.Verify(); err != nil {
		t.Fatal(err)
	}
	s := newStoreNamed(t, a.Name)
	ctx := context.Background()
	if _, err := s.Append(ctx, "t1", store.Batch{Events: []grist.Event{{Type: "number", Version: 2, Body: json.RawMessage(`{"future":"shape"}`)}}, Cause: "one", Step: projectionRow(t, "[42]", 2)}); err != nil {
		t.Fatal(err)
	}
	sink := &qobs.MemorySink{}
	relay, err := grist.NewRelay(s)
	if err != nil {
		t.Fatal(err)
	}
	relay.RenderAs(a)
	if err = relay.Flush(qobs.WithRecorder(ctx, qobs.NewRecorder(sink))); err != nil {
		t.Fatal(err)
	}
	if len(sink.Steps()) != 1 || sink.Steps()[0].Obs["ints"] != "[42]" {
		t.Errorf("newer writer evidence altered: %v", sink.Steps())
	}
}

// The effect can still hold the bytes passed to OnSuccess. Recording the
// preflighted event requires freezing those bytes before invoking user code.
func TestProjectionEffectCannotMutateValidatedOutcome(t *testing.T) {
	for _, guarantee := range []grist.Guarantee{grist.AtLeastOnce, grist.AtMostOnce} {
		t.Run(fmt.Sprint(guarantee), func(t *testing.T) {
			a := grist.New("projection")
			added := grist.NewEvent[Added](a, "added")
			number := grist.NewEvent[projectionNumber](a, "number")
			effects := 0
			grist.NewProcessor(a, "project", added, func(context.Context, *grist.Reads, Added, grist.Meta) grist.Result {
				outcome := number.Of(projectionNumber{1})
				return grist.Effect(func(context.Context) error {
					effects++
					for i := range outcome.Body {
						outcome.Body[i] = 'x'
					}
					return nil
				}, guarantee, grist.OnSuccess(outcome))
			})
			if err := a.Verify(); err != nil {
				t.Fatal(err)
			}
			sink := &qobs.MemorySink{}
			sim := qenv.New(qenv.WithContext(qenv.NewSeeded(1), qobs.WithRecorder(context.Background(), qobs.NewRecorder(sink))))
			defer sim.Close()
			sim.Check = true
			s := simstore.New(qenv.NewStore(sim, a.Name, qenv.Serializable))
			ctx := sim.Context()
			if err := s.CreateTenant(ctx, "t1"); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Append(ctx, "t1", store.Batch{Events: []grist.Event{added.Of(Added{By: 1})}}); err != nil {
				t.Fatal(err)
			}
			sim.Process("node", true, func(ctx context.Context) {
				if err := a.Start(ctx, s); err != nil {
					t.Error(err)
				}
			})
			if err := sim.Run(time.Minute); err != nil {
				t.Fatal(err)
			}
			events, _, err := s.Read(ctx, "t1", grist.Selector{Types: []string{"number", "grist.failed", "grist.unknown"}}, 0)
			if err != nil {
				t.Fatal(err)
			}
			if effects != 1 || len(events) != 1 || events[0].Type != "number" {
				t.Fatalf("validated outcome lost: effects=%d events=%v", effects, events)
			}
			value, err := number.Decode(events[0])
			if err != nil || value.Value != 1 {
				t.Errorf("effect mutated preflighted evidence: value=%+v error=%v", value, err)
			}
		})
	}
}

// Aggregate observation lists cannot be split by an unknown event's fields.
// Refuse a required mixed-version rewrite rather than dropping unknown values.
func TestProjectionRelayRefusesPartialMixedRewrite(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(fmt.Sprintf("unknown-type=%v", unknown), func(t *testing.T) {
			a := grist.New("projection")
			grist.NewEvent[projectionNumber](a, "number", grist.Upcast(1, func(v projectionOld) projectionNumber { return projectionNumber{v.Value + 1} }))
			if err := a.Verify(); err != nil {
				t.Fatal(err)
			}
			typ, version := "number", 3
			if unknown {
				typ, version = "future", 1
			}
			step, err := json.Marshal(map[string]any{"v": 1, "steps": []any{map[string]any{"action": "add", "events": 2, "kv": [][]any{
				{"obs.events", []string{"number", typ}}, {"obs.strs", []string{"retain-me"}}, {"obs.ints", []int{4, 99}},
				{"obs.causes", [][]string{{"command", "one"}, {"command", "one"}}}, {"grist.rendered", []int{1, version}},
			}}}})
			if err != nil {
				t.Fatal(err)
			}
			s := newStoreNamed(t, a.Name)
			ctx := context.Background()
			if _, err = s.Append(ctx, "t1", store.Batch{Events: []grist.Event{
				{Type: "number", Version: 1, Body: json.RawMessage(`{"value":4}`)},
				{Type: typ, Version: version, Body: json.RawMessage(`{"future":"retain-me","other":99}`)},
			}, Cause: "one", Step: step}); err != nil {
				t.Fatal(err)
			}
			sink := &qobs.MemorySink{}
			relay, err := grist.NewRelay(s)
			if err != nil {
				t.Fatal(err)
			}
			relay.RenderAs(a)
			if err = relay.Flush(qobs.WithRecorder(ctx, qobs.NewRecorder(sink))); err == nil {
				t.Error("partial mixed rewrite passed without a data error")
			}
			if len(sink.Steps()) != 0 {
				t.Errorf("partial aggregate emitted: %v", sink.Steps())
			}
			if behind, e := relay.Behind(ctx); e != nil || behind != 1 {
				t.Errorf("partial aggregate advanced relay: behind=%d error=%v", behind, e)
			}
		})
	}
}

func TestProjectionCommandRejectsInvalidBodies(t *testing.T) {
	for _, body := range []string{`null`, `{"value":"wrong"}`, `{"value":`} {
		t.Run(body, func(t *testing.T) {
			a := grist.New("projection")
			grist.NewEvent[projectionNumber](a, "number")
			cmd := grist.NewCommand(a, "add", func(*grist.Reads, projectionArgs) ([]grist.Event, error) {
				return []grist.Event{{Type: "number", Version: 1, Body: json.RawMessage(body)}}, nil
			})
			if err := a.Verify(); err != nil {
				t.Fatal(err)
			}
			s := newStoreNamed(t, a.Name)
			ctx := context.Background()
			_, err := cmd.Execute(ctx, s, grist.Envelope{ID: "one", Tenant: "t1"}, projectionArgs{})
			var rejection *grist.Rejected
			if err == nil || errors.Is(err, grist.ErrBadArgs) || errors.As(err, &rejection) {
				t.Errorf("invalid application body: %v", err)
			}
			if _, marked, e := s.Marker(ctx, "t1", "one"); e != nil || marked {
				t.Errorf("bad body marker: marked=%v error=%v", marked, e)
			}
			if _, head, e := s.Read(ctx, "t1", grist.Selector{Types: []string{"number"}}, 0); e != nil || head != 0 {
				t.Errorf("bad body head=%d error=%v", head, e)
			}
		})
	}
}

type projectionQty struct {
	Qty int `json:"qty"`
}
type projectionCount struct {
	Count int `json:"count"`
}

// Rules use the app's newest shape even while a rolling release writes an
// older shape. Downcasting before the rule would silently turn Count into zero.
func TestProjectionRulesSeeNewestBeforeDowncast(t *testing.T) {
	for _, rawOlder := range []bool{false, true} {
		for _, kind := range []string{"emit", "at-least-once", "at-most-once"} {
			for _, count := range []int{3, 5} {
				t.Run(fmt.Sprintf("old-candidate=%v/%s/count=%d", rawOlder, kind, count), func(t *testing.T) {
					a := grist.New("projection")
					added := grist.NewEvent[Added](a, "added")
					counted := grist.NewEvent[projectionCount](a, "counted",
						grist.Upcast(1, func(v projectionQty) projectionCount { return projectionCount{v.Qty} }),
						grist.WritesVersion(1, func(v projectionCount) projectionQty { return projectionQty{v.Count} }))
					effects := 0
					guarantee := grist.AtLeastOnce
					if kind == "at-most-once" {
						guarantee = grist.AtMostOnce
					}
					option := grist.Emits(counted)
					if kind != "emit" {
						option = grist.Delivers(guarantee, counted)
					}
					grist.NewProcessor(a, "project", added, func(context.Context, *grist.Reads, Added, grist.Meta) grist.Result {
						event := counted.Of(projectionCount{count})
						if rawOlder {
							event = grist.Event{Type: "counted", Version: 1, Body: json.RawMessage(fmt.Sprintf(`{"qty":%d}`, count))}
						}
						if kind == "emit" {
							return grist.Emit(event)
						}
						return grist.Effect(func(context.Context) error { effects++; return nil }, guarantee, grist.OnSuccess(event))
					}, option)
					refusal := grist.Rejection("too-many")
					var seen []int
					grist.NewRule(a, "count-limit", counted, func(r *grist.Reads, e projectionCount) error {
						seen = append(seen, e.Count)
						if e.Count > 4 {
							return refusal
						}
						return nil
					}, grist.Refuses(refusal))
					if err := a.Verify(); err != nil {
						t.Fatal(err)
					}
					sink := &qobs.MemorySink{}
					sim := qenv.New(qenv.WithContext(qenv.NewSeeded(1), qobs.WithRecorder(context.Background(), qobs.NewRecorder(sink))))
					defer sim.Close()
					sim.Check = true
					s := simstore.New(qenv.NewStore(sim, a.Name, qenv.Serializable))
					ctx := sim.Context()
					if err := s.CreateTenant(ctx, "t1"); err != nil {
						t.Fatal(err)
					}
					if _, err := s.Append(ctx, "t1", store.Batch{Events: []grist.Event{added.Of(Added{By: 1})}}); err != nil {
						t.Fatal(err)
					}
					sim.Process("node", true, func(ctx context.Context) {
						if err := a.Start(ctx, s); err != nil {
							t.Error(err)
						}
					})
					if err := sim.Run(time.Minute); err != nil {
						t.Fatal(err)
					}
					events, _, err := s.Read(ctx, "t1", grist.Selector{Types: []string{"counted", "grist.failed"}}, 0)
					if err != nil {
						t.Fatal(err)
					}
					if len(seen) == 0 {
						t.Error("rule did not judge the candidate")
					}
					for _, value := range seen {
						if value != count {
							t.Errorf("rule saw Count=%d, want newest Count=%d", value, count)
						}
					}
					if count > 4 {
						if effects != 0 || len(events) != 1 || events[0].Type != "grist.failed" {
							t.Errorf("rule refusal effects=%d events=%v", effects, events)
						}
						diagnostic := false
						for _, step := range sink.Steps() {
							if strings.Contains(step.Error, "rule count-limit refuses (too-many)") {
								diagnostic = true
							}
						}
						if !diagnostic {
							t.Error("rule refusal diagnosis not retained")
						}
					} else {
						expectedEffects := 1
						if kind == "emit" {
							expectedEffects = 0
						}
						if effects != expectedEffects || len(events) != 1 || events[0].Type != "counted" || events[0].Version != 1 {
							t.Fatalf("admitted rolling write effects=%d events=%v", effects, events)
						}
						var old projectionQty
						if err := json.Unmarshal(events[0].Body, &old); err != nil || old.Qty != count {
							t.Errorf("stored old shape=%+v error=%v", old, err)
						}
					}
				})
			}
		}
	}
}

// Invalid application evidence must not be disguised as a remembered business
// refusal simply because a rule would also reject the attempted append.
func TestProjectionCommandInvalidSourcePrecedesRules(t *testing.T) {
	a := grist.New("projection")
	number := grist.NewEvent[projectionNumber](a, "number")
	cmd := grist.NewCommand(a, "add", func(*grist.Reads, projectionArgs) ([]grist.Event, error) {
		return []grist.Event{number.Of(projectionNumber{math.MaxUint64})}, nil
	}, grist.Appends(number))
	refusal := grist.Rejection("not-admitted")
	ruleCalls := 0
	grist.NewRule(a, "deny", number, func(*grist.Reads, projectionNumber) error { ruleCalls++; return refusal }, grist.Refuses(refusal))
	if err := a.Verify(); err != nil {
		t.Fatal(err)
	}
	s := newStoreNamed(t, a.Name)
	ctx := context.Background()
	_, err := cmd.Execute(ctx, s, grist.Envelope{ID: "one", Tenant: "t1"}, projectionArgs{})
	var rejection *grist.Rejected
	if err == nil || errors.Is(err, grist.ErrBadArgs) || errors.As(err, &rejection) || !strings.Contains(err.Error(), "int64") {
		t.Errorf("invalid source became a business outcome: %v", err)
	}
	if ruleCalls != 0 {
		t.Errorf("rule judged invalid source %d times", ruleCalls)
	}
	if _, marked, e := s.Marker(ctx, "t1", "one"); e != nil || marked {
		t.Errorf("bad source marker: marked=%v error=%v", marked, e)
	}
	if _, head, e := s.Read(ctx, "t1", grist.Selector{Types: []string{"number"}}, 0); e != nil || head != 0 {
		t.Errorf("bad source head=%d error=%v", head, e)
	}
}

func TestProjectionCommandRulesNormalizeOlderCandidate(t *testing.T) {
	for _, count := range []int{3, 5} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			a := grist.New("projection")
			counted := grist.NewEvent[projectionCount](a, "counted",
				grist.Upcast(1, func(v projectionQty) projectionCount { return projectionCount{v.Qty} }),
				grist.WritesVersion(1, func(v projectionCount) projectionQty { return projectionQty{v.Count} }))
			cmd := grist.NewCommand(a, "add", func(*grist.Reads, projectionArgs) ([]grist.Event, error) {
				return []grist.Event{{Type: "counted", Version: 1, Body: json.RawMessage(fmt.Sprintf(`{"qty":%d}`, count))}}, nil
			}, grist.Appends(counted))
			refusal := grist.Rejection("too-many")
			seen := -1
			grist.NewRule(a, "count-limit", counted, func(r *grist.Reads, e projectionCount) error {
				seen = e.Count
				if e.Count > 4 {
					return refusal
				}
				return nil
			}, grist.Refuses(refusal))
			if err := a.Verify(); err != nil {
				t.Fatal(err)
			}
			s := newStoreNamed(t, a.Name)
			ctx := context.Background()
			_, err := cmd.Execute(ctx, s, grist.Envelope{ID: "one", Tenant: "t1"}, projectionArgs{})
			if seen != count {
				t.Errorf("rule saw newestCount=%d, want%d", seen, count)
			}
			events, head, rerr := s.Read(ctx, "t1", grist.Selector{Types: []string{"counted"}}, 0)
			if rerr != nil {
				t.Fatal(rerr)
			}
			marker, marked, merr := s.Marker(ctx, "t1", "one")
			if merr != nil || !marked {
				t.Fatalf("command marker: present=%v error=%v", marked, merr)
			}
			if count > 4 {
				if !errors.Is(err, refusal) || head != 0 || len(events) != 0 || marker.Outcome != "too-many" {
					t.Errorf("older candidate refusal: error=%v head=%d events=%v marker=%+v", err, head, events, marker)
				}
			} else if err != nil || head != 1 || len(events) != 1 || events[0].Version != 1 {
				t.Errorf("older candidate admission: error=%v head=%d events=%v", err, head, events)
			}
		})
	}
}
