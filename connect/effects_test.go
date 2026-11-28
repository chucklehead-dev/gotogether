package connect

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/itf"
	"github.com/chucklehead-dev/gotogether/qobs"
)

// The effects tests (DESIGN §115) run testdata/sender.qnt: a gateway
// whose attempt sends a request to the deployment it names, with that
// deployment's model, and says hello when it starts. Each mutant of it
// is caught by its effects alone: the state it keeps is the model's, so
// with Effects removed (the ablation) each passes.

// senderModels are the models the deployments serve (sender.qnt's MODELS).
var senderModels = map[string]string{"d1": "m1", "d2": "m2"}

// gateway is sender.qnt's implementation, with a mutant planted (or none).
type gateway struct {
	mutant    string
	attempted map[string]string
	ended     map[string]bool
	first     map[string]string // each call's first deployment
	late      sync.WaitGroup    // the late mutant's goroutines
	release   []chan struct{}   // each held until its step's effects are judged
}

type gatewayState struct {
	Attempted map[string]string
	Ended     map[string]bool
}

func newGateway(mutant string) func(context.Context, testing.TB) *gateway {
	return func(ctx context.Context, _ testing.TB) *gateway {
		if mutant != "noHello" {
			qobs.Effect(ctx, "upstream.hello")
		}
		return &gateway{mutant: mutant, attempted: map[string]string{}, ended: map[string]bool{}, first: map[string]string{}}
	}
}

// errEnded is the duplicate mutant's refusal, after its second send.
var errEnded = errors.New("the gateway ended the call where the model attempts")

func (g *gateway) attempt(ctx context.Context, c, d string) error {
	g.attempted[c] = d
	if _, ok := g.first[c]; !ok {
		g.first[c] = d
	}
	model := senderModels[d]
	if g.mutant == "failover" {
		model = senderModels[g.first[c]] // golem's: the first deployment's model kept in the body
	}
	send := func(ctx context.Context, key string) {
		qobs.Effect(ctx, "upstream.request", "call", c, "deployment", d, "model", model, "key", key)
	}
	key := c + "/" + d
	switch g.mutant {
	case "missing":
	case "duplicate":
		send(ctx, key)
		send(ctx, key)
	case "freshKey":
		send(ctx, key)
		send(ctx, key+"#2")
	case "duplicateFails":
		send(ctx, key)
		send(ctx, key)
		return errEnded
	case "late":
		send(ctx, key)
		// a retry the step did not wait for, held until the step's effects
		// are judged (Check releases it): unheld, it raced the step's return
		// and was sometimes the step's own second send
		gate := make(chan struct{})
		g.release = append(g.release, gate)
		g.late.Go(func() { <-gate; send(ctx, key) })
	case "background":
		send(context.Background(), key) // the gap: no recorder, so no send
	default:
		send(ctx, key)
	}
	return nil
}

func (g *gateway) commit(ctx context.Context, c string) error {
	g.ended[c] = true
	if g.mutant == "commitSends" {
		qobs.Effect(ctx, "upstream.request", "call", c, "deployment", g.attempted[c], "model", senderModels[g.attempted[c]], "key", c+"/commit")
	}
	return nil
}

// senderEffects is the mapping: a Send from each upstream request, a
// Hello from the hello; tries of one request carry one key.
var senderEffects = binding.Effects{Kinds: map[string]binding.EffectKind{
	"Send":  {From: "upstream.request", Fields: map[string]string{"call": "{{.call}}", "deployment": "{{.deployment}}", "model": "{{.model}}"}, Same: []string{"key"}},
	"Hello": {From: "upstream.hello"},
}}

// senderBinding is the gateway bound to sender.qnt, its state projected,
// waiting after each step for the goroutines it started (so that a late
// send lands between two steps); with effects, compared (match for
// attempt's).
func senderBinding(mutant string, effects bool, match string) *Binding[*gateway] {
	b := BindContext(newGateway(mutant)).
		Action("attempt", (*gateway).attempt, "c", "d").
		Action("commit", (*gateway).commit, "c").
		State(func(g *gateway) gatewayState { return gatewayState{Attempted: g.attempted, Ended: g.ended} }).
		Check(func(g *gateway, _ *Step) error {
			for _, gate := range g.release {
				close(gate)
			}
			g.release = nil
			g.late.Wait()
			return nil
		})
	if effects {
		b.Effects(senderEffects)
		if match != "" {
			b.EffectsOf("attempt", "", match)
		}
	}
	return b
}

// senderRun is sender.qnt's traces and model, on quint's path or the
// compiled one (testdata/sender.mbt.json.gz), generated once per path.
type senderRun struct {
	c      Config
	traces []*itf.Trace
	ev     *evaluator
}

var senderRuns sync.Map // "quint" | "compiled" -> *senderRun

func sharedSenderRun(t *testing.T, path string) *senderRun {
	t.Helper()
	if r, ok := senderRuns.Load(path); ok {
		return r.(*senderRun)
	}
	if path == "quint" {
		needQuint(t)
	} else {
		noQuint(t)
	}
	dir, err := os.MkdirTemp("", "connect-sender-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	files, c, err := GenerateRun(t.Context(), Config{Spec: "testdata/sender.qnt", MaxSamples: 12, MaxSteps: 6, Seed: "0x5", Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if (c.compiled != nil) != (path == "compiled") {
		t.Fatalf("path %s: compiled %v", path, c.compiled != nil)
	}
	traces, err := ReadTraces(t.Context(), c, files)
	if err != nil {
		t.Fatal(err)
	}
	c.Dir = ""
	r := &senderRun{c: c, traces: traces, ev: &evaluator{ctx: context.Background(), c: c}}
	// every def a test names, in one compile (attemptEffect, which the
	// model lacks, marked missing by the same one)
	if _, err := r.ev.modelWith(append(senderBinding("", true, "").defs(), "helloTwice", "modelOf", "commitTo", "attemptEffect")); err != nil {
		t.Fatal(err)
	}
	senderRuns.Store(path, r)
	return r
}

// replayBound replays every trace of r on b's driver, as Run does after
// its check, and returns the first failure.
func replayBound(t *testing.T, r *senderRun, b *Binding[*gateway]) *StepError {
	t.Helper()
	ft := &fatalTB{TB: t}
	m, err := r.ev.modelWith(b.defs())
	if err != nil {
		t.Fatal(err)
	}
	if bad, _, _ := b.checkModel(m, r.c); len(bad) > 0 {
		t.Fatalf("the binding does not fit: %v", bad)
	}
	for i, tr := range r.traces {
		d := b.Driver(ft)
		if ft.fatal != "" {
			t.Fatal(ft.fatal)
		}
		if err := Replay(tr, d, ReplayOptions{Trace: i, eval: r.ev, ctx: t.Context()}); err != nil {
			se, ok := errors.AsType[*StepError](err)
			if !ok {
				t.Fatalf("trace %d: %v", i, err)
			}
			return se
		}
	}
	return nil
}

// Each mutant fails at a step's effects, the message naming the step, the
// effect and the field, and the fix; the faithful gateway conforms; and
// with Effects removed every mutant passes but the one whose step fails
// on its own. On quint's path and the compiled model's, alike.
func TestBindEffects(t *testing.T) {
	for _, path := range []string{"quint", "compiled"} {
		t.Run(path, func(t *testing.T) {
			r := sharedSenderRun(t, path)
			if se := replayBound(t, r, senderBinding("", true, "")); se != nil {
				t.Fatalf("the faithful gateway: %v", se)
			}
			if se := replayBound(t, r, senderBinding("duplicate", true, binding.MatchRetried)); se != nil {
				t.Errorf("a retry under one key, match retried: %v", se)
			}
			for _, c := range []struct {
				mutant, match string
				want          []string
			}{
				{"failover", "", []string{
					`: its effects are not the model's attemptEffects(`,
					"  effects:  not the model's attemptEffects(",
					"(upstream.request at seq ",
					`first difference: effect 1, field model: "m1", expected "m2"`,
					"Fix the code's effect, or attemptEffects if the model is wrong; the effects' fields come from the Binding's Effects.",
				}},
				{"duplicate", "", []string{`first difference: effect 2: Send({ call: "c`, `, expected nothing`}},
				{"freshKey", binding.MatchRetried, []string{
					"(match retried)",
					"was tried again under another key: ",
					"first difference: effect 2",
				}},
				{"missing", "", []string{`first difference: effect 1: nothing, expected Send({ call: "c`}},
				{"noHello", "", []string{"step 0 init: its effects are not the model's initEffects", "first difference: effect 1: nothing, expected Hello"}},
				{"commitSends", "", []string{
					`commit(c=`, "commit made effects, and the model states none (it has no commitEffects)",
					"Fix the code if commit sends nothing, or state what it sends in the model (def commitEffects(...): List[Effect]; [] says it sends nothing); " +
						`EffectsOf("commit", "-", "") compares none of its effects.`,
				}},
				{"duplicateFails", "", []string{
					`first difference: effect 2: Send(`,
					"\n  and the step failed: the gateway ended the call where the model attempts",
				}},
				{"late", "", []string{
					`its effects: upstream.request(call=c`,
					") at seq ",
					"made after step ",
					"(attempt(c=",
					"returned and before this step began: no step's. Make the driver wait for the work its step starts",
				}},
				// an effect sent with context.Background() is recorded
				// nowhere: the documented gap, a missing send that fails
				{"background", "", []string{`first difference: effect 1: nothing, expected Send(`}},
			} {
				t.Run(c.mutant, func(t *testing.T) {
					se := replayBound(t, r, senderBinding(c.mutant, true, c.match))
					if se == nil || se.Kind != "effects" {
						t.Fatalf("want an effects failure, got %v", se)
					}
					msg := se.Error()
					for _, w := range c.want {
						if !strings.Contains(msg, w) {
							t.Errorf("want %q in:\n%s", w, msg)
						}
					}
					if c.mutant == "late" && se.Effects != nil || c.mutant != "late" && se.Effects == nil {
						t.Errorf("Effects %v: a mismatch's, none for effects made between steps", se.Effects)
					}
					if c.mutant == "duplicateFails" && !errors.Is(se, errEnded) {
						t.Errorf("the step's own error is the StepError's Err: %v", se.Err)
					}
					// ablated: the state alone passes it (the step's own
					// error aside, which then fails it as a step)
					ab := replayBound(t, r, senderBinding(c.mutant, false, ""))
					switch {
					case c.mutant == "duplicateFails":
						if ab == nil || ab.Kind != "step" || !errors.Is(ab, errEnded) {
							t.Errorf("ablated: want the step's own error, got %v", ab)
						}
					case ab != nil:
						t.Errorf("ablated: %v", ab)
					}
				})
			}
			// match set: the def's list as a set, two hellos one
			if se := replayBound(t, r, senderBinding("", true, "").EffectsOf("init", "helloTwice", binding.MatchSet)); se != nil {
				t.Errorf("helloTwice as a set: %v", se)
			}
			if se := replayBound(t, r, senderBinding("", true, "").EffectsOf("init", "helloTwice", "")); se == nil || se.Effects == nil || se.Effects.Index != 2 {
				t.Errorf("helloTwice exact: want effect 2 missing, got %v", se)
			}
			// opted out: commit's sends are not compared
			if se := replayBound(t, r, senderBinding("commitSends", true, "").EffectsOf("commit", "-", "")); se != nil {
				t.Errorf("commit opted out: %v", se)
			}
		})
	}
}

// The binding's effects are checked against the model before any trace:
// each refusal a line naming the fix.
func TestBindEffectsChecked(t *testing.T) {
	r := sharedSenderRun(t, "quint")
	base := func() *Binding[*gateway] {
		return BindContext(newGateway("")).Action("attempt", (*gateway).attempt, "c", "d").Action("commit", (*gateway).commit, "c")
	}
	kinds := func(edit func(map[string]binding.EffectKind)) binding.Effects {
		k := map[string]binding.EffectKind{}
		for n, v := range senderEffects.Kinds {
			k[n] = v
		}
		edit(k)
		return binding.Effects{Kinds: k}
	}
	for _, c := range []struct {
		name string
		b    *Binding[*gateway]
		want []string
	}{
		{"faithful", base().Effects(senderEffects).EffectsOf("attempt", "", binding.MatchRetried), nil},
		{"an unknown constructor", base().Effects(kinds(func(k map[string]binding.EffectKind) { k["Sned"] = k["Send"] })),
			[]string{"connect.Bind: Effects maps Sned, which is no constructor of the effects defs' type (initEffects's: Hello, Send): map the model's constructors"}},
		{"fields not the constructor's", base().Effects(kinds(func(k map[string]binding.EffectKind) {
			k["Send"] = binding.EffectKind{From: "upstream.request", Fields: map[string]string{"call": "{{.call}}", "model": "{{.model}}", "key": "{{.key}}"}}
		})),
			[]string{"connect.Bind: Effects' Send maps the fields call, key, model, but the model's Send has call, deployment, model (missing deployment; not the model's key): map exactly its record's fields"}},
		{"a def's foreign parameter", base().Effects(senderEffects).EffectsOf("commit", "commitTo", ""),
			[]string{"connect.Bind: action commit: its effects def commitTo takes d, which commit does not (it takes c): a def's parameters are the action's, by name"}},
		{"a def of no effects", base().Effects(senderEffects).EffectsOf("attempt", "modelOf", ""),
			[]string{"connect.Bind: action attempt: its effects def modelOf is str, no list or set of the model's effect type"}},
		{"a def the model lacks", base().Effects(senderEffects).EffectsOf("attempt", "attemptEffect", ""),
			[]string{`connect.Bind: EffectsOf("attempt"): attemptEffect is no val or def of testdata/sender.qnt: name the def that states attempt's effects`}},
		{"EffectsOf an unbound action", base().Effects(senderEffects).EffectsOf("retry", "", ""),
			[]string{`connect.Bind: EffectsOf("retry"): no action retry is bound (bound: attempt, commit): bind it with Action, Handle or Skip, or name init, the constructor`}},
		{"no mode", base().Effects(senderEffects).EffectsOf("attempt", "", "retry"),
			[]string{`connect.Bind: EffectsOf("attempt"): match "retry" is no mode: give binding.MatchExact, MatchRetried or MatchSet ("" is exact)`}},
		{"EffectsOf without Effects", base().EffectsOf("attempt", "", ""),
			[]string{`connect.Bind: EffectsOf("attempt") without Effects compares nothing: give the Binding's Effects`}},
		{"a mapping with no def", base().Effects(senderEffects).EffectsOf("init", "-", "").EffectsOf("attempt", "-", ""),
			[]string{"connect.Bind: Effects maps Hello, Send, but no action has an effects def to compare with"}},
		{"no From", base().Effects(kinds(func(k map[string]binding.EffectKind) { k["Hello"] = binding.EffectKind{} })),
			[]string{`connect.Bind: Effects' Hello has no From: name the recorded effect kind it maps`}},
	} {
		t.Run(c.name, func(t *testing.T) {
			m, err := r.ev.modelWith(c.b.defs())
			if err != nil {
				t.Fatal(err)
			}
			bad, _, _ := c.b.checkModel(m, r.c)
			if len(bad) != len(c.want) {
				t.Fatalf("%d mismatches, want %d:\n%s", len(bad), len(c.want), strings.Join(bad, "\n"))
			}
			for i, w := range c.want {
				if !strings.HasPrefix(bad[i], w) {
					t.Errorf("mismatch %d:\n  got  %s\n  want %s", i+1, bad[i], w)
				}
			}
		})
	}
}

// b.Run with Effects, end to end: the binding checked before the traces,
// every trace's effects compared, on quint's path and the compiled
// model's.
func TestBindEffectsRun(t *testing.T) {
	c := Config{Spec: "testdata/sender.qnt", MaxSamples: 5, MaxSteps: 6, Seed: "0x9"}
	t.Run("quint", func(t *testing.T) {
		needQuint(t)
		senderBinding("", true, "").Run(t, c)
	})
	t.Run("compiled", func(t *testing.T) {
		noQuint(t)
		senderBinding("", true, binding.MatchRetried).Run(t, c)
	})
}
