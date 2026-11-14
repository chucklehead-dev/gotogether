package limit

import (
	"context"

	"github.com/chucklehead-dev/gotogether/connect"
)

type driver struct{ l *Limiter }

func (d *driver) audit(*connect.Step) error { return d.l.Audit(context.Background()) }

// outage is the environment's: the driver itself releases a call before
// the gateway's own release, a call the test makes, not the code.
func (d *driver) outage(ctx context.Context) error {
	if err := d.l.S.Release(ctx, "k", 1); err != nil {
		return err
	}
	return d.l.Done(ctx, "k")
}

func (d *driver) Step(s *connect.Step) error {
	ctx := context.Background()
	return connect.Switch{
		"init":   func(*connect.Step) error { d.l = &Limiter{S: &Mem{}, Max: 2}; return nil },
		"admit":  func(*connect.Step) error { return d.l.Admit(ctx, "k") },
		"done":   func(*connect.Step) error { return d.l.Done(ctx, "k") },
		"audit":  d.audit,
		"outage": func(*connect.Step) error { return d.outage(ctx) },
		"tick":   connect.Skip,
	}.Step(s)
}
