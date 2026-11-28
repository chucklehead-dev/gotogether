package connect

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/qenv"
)

// upstreamReq is the request a simulated gateway sends: a qenv
// Service's call records it as rpc.upstream.request, its exported
// fields the effect's (qenv.EffectFields).
type upstreamReq struct{ Call, Deployment, Model string }

// simGateway attempts through a qenv Service in a simulation made under
// the step's context, which carries the trace's effects recorder: the
// double records the request at its send, and the step's window takes it.
type simGateway struct {
	wrongModel bool
	attempted  map[string]string
	ended      map[string]bool
}

func (g *simGateway) attempt(ctx context.Context, c, d string) error {
	g.attempted[c] = d
	model := senderModels[d]
	if g.wrongModel {
		model = "m0"
	}
	sim := qenv.New(qenv.WithContext(qenv.Nominal{}, ctx))
	defer sim.Close()
	svc := qenv.NewService(sim, "upstream", nil, func(context.Context, upstreamReq) (string, error) { return "ok", nil })
	var err error
	sim.Spawn("gateway", func(ctx context.Context) {
		_, err = svc.Call(ctx, upstreamReq{Call: c, Deployment: d, Model: model}, time.Second)
	})
	if rerr := sim.Run(5 * time.Second); rerr != nil {
		return rerr
	}
	return err
}

func (g *simGateway) commit(c string) { g.ended[c] = true }

// A qenv Service called in a method, its simulation made under the
// step's context: its request is the step's effect, compared with
// attemptEffects like any other. A wrong model in the request fails at
// it, and passes with Effects removed.
func TestBindEffectsThroughQenv(t *testing.T) {
	fx := binding.Effects{Kinds: map[string]binding.EffectKind{
		"Send":  {From: "rpc.upstream.request", Fields: map[string]string{"call": "{{.Call}}", "deployment": "{{.Deployment}}", "model": "{{.Model}}"}},
		"Hello": {From: "upstream.hello"},
	}}
	bind := func(wrong, effects bool) *Binding[*gateway] {
		b := BindContext(newGateway("")).
			Handle("attempt", func(g *gateway, s *Step) error {
				sg := &simGateway{wrongModel: wrong, attempted: g.attempted, ended: g.ended}
				return sg.attempt(s.Context(), MustPick[string](s, "c"), MustPick[string](s, "d"))
			}).
			Handle("commit", func(g *gateway, s *Step) error {
				(&simGateway{ended: g.ended}).commit(MustPick[string](s, "c"))
				return nil
			}).
			State(func(g *gateway) gatewayState { return gatewayState{Attempted: g.attempted, Ended: g.ended} })
		if effects {
			b.Effects(fx)
		}
		return b
	}
	for _, path := range []string{"quint", "compiled"} {
		t.Run(path, func(t *testing.T) {
			r := sharedSenderRun(t, path)
			if se := replayBound(t, r, bind(false, true)); se != nil {
				t.Fatalf("the faithful gateway through qenv: %v", se)
			}
			se := replayBound(t, r, bind(true, true))
			if se == nil || se.Effects == nil || se.Effects.Field != "model" || se.Effects.Got != `"m0"` {
				t.Fatalf("want the request's model the first difference, got %v", se)
			}
			if !strings.Contains(se.Effects.MadeAt[0], "rpc.upstream.request at seq ") {
				t.Errorf("made at %v: want the Service's request", se.Effects.MadeAt)
			}
			if ab := replayBound(t, r, bind(true, false)); ab != nil {
				t.Errorf("ablated: %v", ab)
			}
		})
	}
}
