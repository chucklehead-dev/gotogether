package fleetrollout

import (
	"context"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/utils/ptr"

	"github.com/chucklehead-dev/gotogether/examples/sktrace"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qenv/kubesim"
)

// Replay plays a SimKube trace's Deployment scalings onto the fleet's web
// Deployments, as another writer, while the rollout runs: each traced
// Deployment is mapped to a member (in the order they first appear), and
// each applied version sets its member's replicas (at most 6), at the
// trace's time divided by speed. Writes go through the members' clients,
// with their faults, and retry a conflict or a failure as a controller
// would (read, modify, update).
func Replay(s *qenv.Sim, f *kubesim.Fleet, tr *sktrace.Trace, speed float64) {
	members := map[string]*kubesim.Cluster{}
	s.Process("workload", false, func(ctx context.Context) {
		ctx = kubesim.AsWriter(ctx, "workload")
		start := qenv.Now(ctx)
		for _, e := range tr.Events {
			at := start.Add(time.Duration(float64(tr.Offset(e)) / speed))
			if d := at.Sub(qenv.Now(ctx)); d > 0 {
				qenv.Sleep(ctx, d)
			}
			for _, o := range e.Applied {
				if o.Kind() != "Deployment" {
					continue
				}
				m, ok := members[o.Key()]
				if !ok {
					m = f.Members[len(members)%len(f.Members)]
					members[o.Key()] = m
				}
				scale(ctx, m, int32(min(max(sktrace.Replicas(o), 1), 6)))
			}
		}
	})
}

// scale sets m's web Deployment's replicas as another writer would.
func scale(ctx context.Context, m *kubesim.Cluster, replicas int32) {
	for range 5 {
		d := &appsv1.Deployment{}
		if err := m.Client.Get(ctx, Web, d); err != nil {
			continue
		}
		if *d.Spec.Replicas == replicas {
			return
		}
		d.Spec.Replicas = ptr.To(replicas)
		if err := m.Client.Update(ctx, d); err == nil {
			return
		}
		qenv.Sleep(ctx, 100*time.Millisecond)
	}
}
