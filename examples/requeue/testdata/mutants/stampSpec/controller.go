// Package requeue is a controller whose requeue can keep disabling the
// step it waits for: it applies a cloud resource's spec and requeues
// until the cloud reports the resource ready, then marks its object
// ready. The cloud provisions in two steps and starts over when the spec
// it is given changes. The faithful controller applies the same spec
// every time; its mutant stampSpec puts the reconcile's time in it, so
// that every requeue replaces the resource and provisioning never ends: a
// retry storm that weak fairness does not rule out (model/requeue.qnt's
// RESEND), found in the model by `gotogether simulate -graph` and shown
// by its runs as a hot cycle.
package requeue

import (
	"context"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
)

// Controller reconciles one object against the cloud.
type Controller struct {
	Cloud *qenv.Service[Apply, string]
	// RequeueAfter is the wait before the next reconcile, Timeout each
	// apply's
	RequeueAfter, Timeout time.Duration
}

// spec is the resource's spec as the controller applies it: the object's
// desired state, and (the mutant) the time of the reconcile, a field
// someone added to see when the controller last looked. The cloud sees a
// new spec at every apply and replaces the resource.
func (c *Controller) spec(ctx context.Context) Apply {
	return Apply{Spec: "db: small\nrequestedAt: " + qenv.Now(ctx).Format(time.RFC3339Nano)}
}

// Reconcile applies the spec and says whether the resource is ready:
// then the object is marked ready (finish), else it is requeued.
func (c *Controller) Reconcile(ctx context.Context) bool {
	phase, err := c.Cloud.Call(ctx, c.spec(ctx), c.Timeout)
	if err == nil && phase == "ready" {
		qobs.Record(ctx, "finish", nil)
		return true
	}
	qobs.Record(ctx, "requeue", nil)
	return false
}

// Run reconciles until the object is ready, RequeueAfter between tries.
func (c *Controller) Run(ctx context.Context) {
	for !c.Reconcile(ctx) {
		qenv.Sleep(ctx, c.RequeueAfter)
	}
}

// Run deploys the cloud and the controller on c: five seconds of faults,
// then a settle (faults off) of a minute, whose error is the run's (an
// *qenv.Unsettled when the controller is still requeueing).
func Run(c qenv.Chooser) error {
	s := qenv.New(c)
	defer s.Close()
	cloud := NewCloud(s, time.Second)
	ctrl := &Controller{Cloud: cloud.Svc, RequeueAfter: 1500 * time.Millisecond, Timeout: 500 * time.Millisecond}
	s.Spawn("controller", ctrl.Run)
	if err := s.Run(5 * time.Second); err != nil {
		return err
	}
	return s.Settle(time.Minute)
}
