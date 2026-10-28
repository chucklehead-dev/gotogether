package requeue

import (
	"context"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
)

// Cloud is the cloud API the controller reconciles against, as a double:
// one resource, applied by spec over a lossy network (qenv.Service), and
// provisioned in two steps of Step each (pending, warming, ready). An
// apply of the spec it holds changes nothing; an apply of another spec
// replaces the resource, whose provisioning starts over. It records
// create and advance where they take effect, so a step the controller
// never heard of (a lost response) is in the run.
type Cloud struct {
	Step time.Duration
	Svc  *qenv.Service[Apply, string]

	phase string // "none", "pending", "warming", "ready"
	spec  string
	gen   int // bumped by each replacement: a provisioning step under way is void
}

// Apply is a request to make the resource match Spec; its response is
// the resource's phase after it.
type Apply struct {
	Spec string
}

// NewCloud starts the cloud's service and its provisioner on s.
func NewCloud(s *qenv.Sim, step time.Duration) *Cloud {
	c := &Cloud{Step: step, phase: "none"}
	c.Svc = qenv.NewService(s, "cloud", nil, c.apply)
	s.Spawn("provisioner", c.provision)
	return c
}

func (c *Cloud) apply(ctx context.Context, req Apply) (string, error) {
	if c.phase == "none" || req.Spec != c.spec {
		c.phase, c.spec = "pending", req.Spec
		c.gen++
		qobs.Record(ctx, "create", nil)
	}
	return c.phase, nil
}

// provision advances the resource a step at a time, each Step after the
// last with no replacement between: idle (a daemon's wait) when there is
// nothing to provision.
func (c *Cloud) provision(ctx context.Context) {
	for {
		qenv.WaitIdle(ctx, func() bool { return c.phase == "pending" || c.phase == "warming" })
		gen := c.gen
		qenv.Sleep(ctx, c.Step)
		if c.gen != gen {
			continue // replaced meanwhile: its provisioning started over
		}
		if c.phase == "pending" {
			c.phase = "warming"
		} else {
			c.phase = "ready"
		}
		c.gen++
		qobs.Record(ctx, "advance", nil)
	}
}
