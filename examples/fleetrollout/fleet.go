// Package fleetrollout runs the rollout controller (package rollout)
// against a simulated fleet (qenv/kubesim) in a qenv simulation: member
// clusters whose API servers fail requests and lose responses, whose
// Deployment status lags its spec, and a controller process that crashes
// and restarts. cmd/fleet-controller runs the same controller against
// real clusters (kwok) through multicluster-runtime.
package fleetrollout

import (
	"context"
	"fmt"
	"strconv"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/chucklehead-dev/gotogether/examples/fleetrollout/rollout"
	"github.com/chucklehead-dev/gotogether/examples/sktrace"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qenv/kubesim"
	"github.com/chucklehead-dev/gotogether/qobs"
)

// Config of a simulated rollout.
type Config struct {
	Members  int // default 3
	Mutation rollout.Mutation
	// NoCrashes keeps the controller up.
	NoCrashes bool
	// Workload, if set, is a production trace replayed onto the fleet as
	// another writer while the rollout runs (Replay), Speed
	// times faster (default 60: a trace's minute in a second).
	Workload *sktrace.Trace
	Speed    float64
	// PodTimes are the members' pod readiness options (kubesim.Cluster):
	// e.g. a trace's lifetimes, Speed times faster.
	PodTimes []time.Duration
}

// Target is the image rolled out; members start at web:v1.
const Target = "web:v2"

// Web is the Deployment every member runs: default/web, 3 replicas.
var Web = client.ObjectKey{Namespace: "default", Name: "web"}

// newMember is a member cluster running Web at web:v1, whose API server
// records each update it applies to Web where it lands: the rollout's as
// patch steps, another writer's (kubesim.AsWriter) as scale steps.
func newMember(s *qenv.Sim, name string) *kubesim.Cluster {
	m := kubesim.NewCluster(s, name, nil, kubesim.NewDeployment(Web, "web:v1", 3))
	m.OnWrite = func(ctx context.Context, w kubesim.Write) {
		d, ok := w.Object.(*appsv1.Deployment)
		if !ok || w.Verb != "update" || w.Subresource != "" || client.ObjectKeyFromObject(d) != Web {
			return
		}
		if w.Writer != "" {
			rv, _ := strconv.ParseInt(d.ResourceVersion, 10, 64)
			qobs.Record(ctx, "scale", nil, "cluster", name, "rv", rv, "generation", d.Generation, "writer", w.Writer)
			return
		}
		rollout.RecordPatch(ctx, name, d)
	}
	return m
}

// Run rolls Target out across the simulated fleet, with c making the
// choices: 20 s of virtual time with faults, a settle of up to 30 s
// without, then a fresh read of every member (observe steps) and settle.
func Run(c qenv.Chooser, cfg Config) (*kubesim.Fleet, error) {
	if cfg.Members == 0 {
		cfg.Members = 3
	}
	s := qenv.New(c)
	defer s.Close()
	f := &kubesim.Fleet{}
	for i := 1; i <= cfg.Members; i++ {
		m := newMember(s, fmt.Sprintf("m%d", i))
		m.PodTimes = cfg.PodTimes
		f.Members = append(f.Members, m)
	}
	if cfg.Workload != nil {
		if cfg.Speed == 0 {
			cfg.Speed = 60
		}
		Replay(s, f, cfg.Workload, cfg.Speed)
	}
	newController := func() *rollout.Controller {
		return &rollout.Controller{Clusters: f, Key: Web, Image: Target, MaxUnavailable: 1, Mutation: cfg.Mutation}
	}
	// a level-based controller: reconcile, wait, repeat; a restart starts
	// a new Controller, with nothing remembered
	s.Process("controller", !cfg.NoCrashes, func(ctx context.Context) {
		ctl := newController()
		qenv.Daemon(ctx) // a controller reconciles forever
		for {
			_, _ = ctl.Reconcile(ctx)
			qenv.Sleep(ctx, 200*time.Millisecond)
		}
	})
	if err := s.Run(20 * time.Second); err != nil {
		return f, err
	}
	if err := s.Settle(30 * time.Second); err != nil {
		return f, err
	}
	final := newController()
	for _, m := range f.Members {
		d := &appsv1.Deployment{}
		_ = m.Stored(Web, d)
		final.Observe(s.Context(), m.Name, d)
	}
	qobs.Record(s.Context(), "settle", nil)
	return f, nil
}
