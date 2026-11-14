package simvalkey

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtest"
	"github.com/valkey-io/valkey-go"
)

func admissionSystem(split bool) qtest.System {
	return qtest.System{Binding: "testdata/slots.yaml", Rates: qenv.NoFaults, Opts: []qtest.Option{qtest.Linearize()}, Run: func(chooser qenv.Chooser) error {
		s := qenv.New(chooser)
		s.Check = true
		defer s.Close()
		v := New(s, "slots")
		var problem error
		for range 2 {
			s.Spawn("admit", func(ctx context.Context) {
				c := v.Client()
				call := qobs.Invoke(ctx, "admit")
				var result int64
				var err error
				if split {
					// Negative control: splitting the script into separate round trips
					// permits both callers to observe zero before either takes the slot.
					text, e := c.Do(ctx, c.B().Get().Key("slots").Build()).ToString()
					n := int64(0)
					if e == nil {
						n, e = strconv.ParseInt(text, 10, 64)
					}
					if e != nil && !valkey.IsValkeyNil(e) {
						err = e
					} else if n == 0 {
						err = c.Do(ctx, c.B().Set().Key("slots").Value("1").Build()).Error()
						result = 1
					}
				} else {
					result, err = valkey.NewLuaScript(admission).Exec(ctx, c, []string{"slots"}, nil).ToInt64()
				}
				if err != nil {
					problem = errors.Join(problem, err)
				}
				call.Done(err, "ok", result == 1)
			})
		}
		if err := s.Run(time.Second); err != nil {
			return err
		}
		return problem
	}}
}

func TestAdmissionModel(t *testing.T) {
	qtest.RequireQuint(t)
	admissionSystem(false).Explore(t, qtest.ExploreConfig{Seeds: 12}).Expect(t, qtest.Conforms())
	admissionSystem(true).Explore(t, qtest.ExploreConfig{Seeds: 12}).Expect(t, qtest.Some(qtest.Rejects("admit")))
}
