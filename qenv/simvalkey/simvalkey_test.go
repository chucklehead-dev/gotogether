package simvalkey

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/valkey-io/valkey-go"
)

const admission = `local n=tonumber(redis.call('GET',KEYS[1]) or '0'); if n<1 then redis.call('SET',KEYS[1],n+1); return 1 end; return 0`

func run(t *testing.T, chooser qenv.Chooser, fn func(context.Context, *Server, *Client)) {
	t.Helper()
	s := qenv.New(chooser)
	s.Check = true
	defer s.Close()
	v := New(s, "db")
	done := false
	s.Spawn("client", func(ctx context.Context) { fn(ctx, v, v.Client()); done = true })
	if err := s.Run(5 * time.Second); err != nil {
		t.Error(err)
	}
	if !done {
		t.Error("client did not finish")
	}
	if err := s.Settle(time.Second); err != nil {
		t.Error(err)
	}
}

func TestLuaClientAtomicAdmission(t *testing.T) {
	for seed := uint64(1); seed <= 12; seed++ {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			chooser := qenv.NewSeeded(seed)
			chooser.Rates = qenv.NoFaults
			s := qenv.New(chooser)
			s.Check = true
			defer s.Close()
			v := New(s, "db")
			done := 0
			accepted := int64(0)
			for range 2 {
				s.Spawn("admit", func(ctx context.Context) {
					c := v.Client()
					n, err := valkey.NewLuaScript(admission).Exec(ctx, c, []string{"slots"}, nil).ToInt64()
					if err != nil {
						t.Error(err)
					}
					accepted += n
					done++
				})
			}
			if err := s.Run(time.Second); err != nil {
				t.Error(err)
			}
			if done != 2 || accepted != 1 {
				t.Errorf("done=%d accepted=%d", done, accepted)
			}
		})
	}
}

func TestServerClockAndExpiry(t *testing.T) {
	s := qenv.New(&qenv.Script{Faults: []qenv.Scripted{{Kind: "clock.skew", Name: "ahead"}, {Kind: "clock.skew", Name: "ahead"}}})
	s.Clocks = qenv.Clocks{Skew: time.Hour}
	s.Check = true
	defer s.Close()
	v := New(s, "db")
	done := false
	s.Process("gateway", false, func(ctx context.Context) {
		c := v.Client()
		parts, err := c.Do(ctx, c.B().Time().Build()).AsStrSlice()
		if err != nil {
			t.Error(err)
			return
		}
		seconds, _ := strconv.ParseInt(parts[0], 10, 64)
		if seconds != s.Now().Unix() || qenv.Now(ctx).Sub(s.Now()) != time.Hour {
			t.Errorf("TIME=%d server=%v caller=%v", seconds, s.Now(), qenv.Now(ctx))
		}
		if err := c.Do(ctx, c.B().Arbitrary("SET", "k", "v", "PX", "10").Build()).Error(); err != nil {
			t.Error(err)
		}
		qenv.Sleep(ctx, 10*time.Millisecond)
		if err := c.Do(ctx, c.B().Get().Key("k").Build()).Error(); !valkey.IsValkeyNil(err) {
			t.Errorf("expiry boundary: %v", err)
		}
		done = true
	})
	if err := s.Run(time.Second); err != nil {
		t.Error(err)
	}
	if !done {
		t.Error("gateway did not finish")
	}
}

func TestOutageHangAndLostResponse(t *testing.T) {
	for _, fault := range []string{"outage", "hang", "lost"} {
		t.Run(fault, func(t *testing.T) {
			kind := "request"
			if fault == "lost" {
				kind = "response"
			}
			run(t, &qenv.Script{Faults: []qenv.Scripted{{Kind: "valkey.db." + kind, Name: fault}}}, func(ctx context.Context, v *Server, c *Client) {
				bounded, cancel := WithTimeout(ctx, 20*time.Millisecond)
				defer cancel()
				err := c.Do(bounded, c.B().Set().Key("k").Value("v").Build()).Error()
				if fault == "outage" {
					if !errors.Is(err, ErrUnavailable) || errors.Is(err, ErrUnknownOutcome) {
						t.Errorf("outage=%v", err)
					}
				} else {
					if !errors.Is(err, context.DeadlineExceeded) || !errors.Is(err, ErrUnknownOutcome) {
						t.Errorf("nonresponse=%v", err)
					}
				}
				got, e := c.Do(ctx, c.B().Get().Key("k").Build()).ToString()
				if fault == "lost" {
					if e != nil || got != "v" {
						t.Errorf("lost reply lost write: %s %v", got, e)
					}
				} else if !valkey.IsValkeyNil(e) {
					t.Errorf("unprocessed request applied: %s %v", got, e)
				}
			})
		})
	}
}

func TestCancelAndParentDeadline(t *testing.T) {
	run(t, &qenv.Script{Faults: []qenv.Scripted{{Kind: "valkey.db.request", Name: "hang"}}}, func(ctx context.Context, v *Server, c *Client) {
		parent, stop := WithTimeout(ctx, 10*time.Millisecond)
		defer stop()
		child, stopChild := WithTimeout(parent, time.Hour)
		defer stopChild()
		err := c.Do(child, c.B().Get().Key("k").Build()).Error()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("parent deadline=%v", err)
		}
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if err := c.Do(canceled, c.B().Set().Key("k").Value("bad").Build()).Error(); !errors.Is(err, context.Canceled) || errors.Is(err, ErrUnknownOutcome) {
			t.Errorf("pre-cancel=%v", err)
		}
		if err := c.Do(ctx, c.B().Get().Key("k").Build()).Error(); !valkey.IsValkeyNil(err) {
			t.Errorf("canceled command wrote: %v", err)
		}
	})
}

func TestReplicaLossAndWait(t *testing.T) {
	run(t, &qenv.Script{Faults: []qenv.Scripted{{Kind: "valkey.db.replication", Name: "lag"}, {Kind: "valkey.db.replication", Name: "lag"}}}, func(ctx context.Context, v *Server, c *Client) {
		if e := c.Do(ctx, c.B().Set().Key("lost").Value("v").Build()).Error(); e != nil {
			t.Error(e)
		}
		n, e := c.Do(ctx, c.B().Arbitrary("WAIT", "1", "10").Build()).ToInt64()
		if e != nil || n != 0 {
			t.Errorf("unreplicated WAIT=%d %v", n, e)
		}
		v.Failover()
		if e := c.Do(ctx, c.B().Get().Key("lost").Build()).Error(); !valkey.IsValkeyNil(e) {
			t.Errorf("failover retained unreplicated value: %v", e)
		}
		if e := c.Do(ctx, c.B().Set().Key("kept").Value("v").Build()).Error(); e != nil {
			t.Error(e)
		}
		v.Replicate()
		v.Failover()
		got, e := c.Do(ctx, c.B().Get().Key("kept").Build()).ToString()
		if e != nil || got != "v" {
			t.Errorf("replicated value lost: %s %v", got, e)
		}
	})
}

func TestParentCloseRejectsDedicatedClient(t *testing.T) {
	run(t, qenv.Nominal{}, func(ctx context.Context, v *Server, c *Client) {
		d, release := c.Dedicate()
		defer release()
		c.Close()
		if err := d.Do(ctx, d.B().Set().Key("k").Value("v").Build()).Error(); !errors.Is(err, valkey.ErrClosing) {
			t.Errorf("dedicated after parent close=%v", err)
		}
	})
}

func TestWaitZeroDoesNotInventReplicaAcknowledgement(t *testing.T) {
	run(t, &qenv.Script{Faults: []qenv.Scripted{{Kind: "valkey.db.replication", Name: "lag"}, {Kind: "valkey.db.replication", Name: "lag"}}}, func(ctx context.Context, v *Server, c *Client) {
		if e := c.Do(ctx, c.B().Set().Key("k").Value("v").Build()).Error(); e != nil {
			t.Error(e)
		}
		n, e := c.Do(ctx, c.B().Arbitrary("WAIT", "0", "0").Build()).ToInt64()
		if e != nil || n != 0 {
			t.Errorf("WAIT0 acknowledged pending write: %d %v", n, e)
		}
		v.Replicate()
		n, e = c.Do(ctx, c.B().Arbitrary("WAIT", "0", "0").Build()).ToInt64()
		if e != nil || n != 1 {
			t.Errorf("WAIT0 after replication: %d %v", n, e)
		}
	})
}

func TestVirtualDeadlineThroughStandardContextChildren(t *testing.T) {
	for _, values := range []bool{false, true} {
		t.Run(fmt.Sprint(values), func(t *testing.T) {
			run(t, &qenv.Script{Faults: []qenv.Scripted{{Kind: "valkey.db.request", Name: "hang"}}}, func(ctx context.Context, v *Server, c *Client) {
				timed, stop := WithTimeout(ctx, time.Millisecond)
				defer stop()
				parent := timed
				if values {
					parent = context.WithValue(parent, struct{ tag int }{1}, "value")
				}
				child, cancel := context.WithCancel(parent)
				defer cancel()
				err := c.Do(child, c.B().Get().Key("k").Build()).Error()
				if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
					t.Errorf("deadline through child=%v", err)
				}
			})
		})
	}
}

func TestCrashedCallerDoesNotLeaveBlockedWait(t *testing.T) {
	s := qenv.New(&qenv.Script{Faults: []qenv.Scripted{{Kind: "valkey.db.replication", Name: "lag"}, {Kind: "valkey.db.replication", Name: "lag"}}})
	s.Check = true
	defer s.Close()
	v := New(s, "db")
	waiting := false
	p := s.Process("caller", true, func(ctx context.Context) {
		c := v.Client()
		if e := c.Do(ctx, c.B().Set().Key("k").Value("v").Build()).Error(); e != nil {
			t.Error(e)
			return
		}
		waiting = true
		c.Do(ctx, c.B().Arbitrary("WAIT", "1", "0").Build())
	})
	s.Spawn("operator", func(ctx context.Context) {
		qenv.Wait(ctx, func() bool { return waiting })
		qenv.Sleep(ctx, time.Millisecond)
		p.Crash()
		p.Restart()
	})
	if err := s.Run(10 * time.Millisecond); err != nil {
		t.Error(err)
	}
	if err := s.Settle(time.Millisecond); err != nil {
		t.Errorf("crashed connection retained WAIT: %v", err)
	}
}

func TestCanceledHangRemainsCallerCancellation(t *testing.T) {
	run(t, &qenv.Script{Faults: []qenv.Scripted{{Kind: "valkey.db.request", Name: "hang"}}}, func(ctx context.Context, v *Server, c *Client) {
		child, cancel := context.WithCancel(ctx)
		qenv.Go(ctx, "cancel", func(ctx context.Context) { qenv.Sleep(ctx, time.Millisecond); cancel() })
		err := c.Do(child, c.B().Get().Key("k").Build()).Error()
		if !errors.Is(err, context.Canceled) || errors.Is(err, ErrUnavailable) || !errors.Is(err, ErrUnknownOutcome) {
			t.Errorf("caller cancellation=%v", err)
		}
	})
}

func TestPipelineTailCanApplyBeforeLostReply(t *testing.T) {
	run(t, &qenv.Script{Faults: []qenv.Scripted{{Kind: "valkey.db.response", Name: "lost"}}}, func(ctx context.Context, v *Server, c *Client) {
		timed, cancel := WithTimeout(ctx, time.Millisecond)
		defer cancel()
		results := c.DoMulti(timed, c.B().Set().Key("a").Value("one").Build(), c.B().Set().Key("b").Value("two").Build())
		for _, r := range results {
			if e := r.Error(); !errors.Is(e, ErrUnknownOutcome) || !errors.Is(e, context.DeadlineExceeded) {
				t.Errorf("pipeline=%v", e)
			}
		}
		for k, want := range map[string]string{"a": "one", "b": "two"} {
			got, e := c.Do(ctx, c.B().Get().Key(k).Build()).ToString()
			if e != nil || got != want {
				t.Errorf("pipeline %s=%s %v", k, got, e)
			}
		}
	})
}

func TestPipelineCommandsHaveInterleavingBoundary(t *testing.T) {
	witnessed := false
	for seed := uint64(1); seed <= 32; seed++ {
		chooser := qenv.NewSeeded(seed)
		chooser.Rates = qenv.NoFaults
		s := qenv.New(chooser)
		s.Check = true
		v := New(s, "db")
		done := false
		s.Spawn("pipeline", func(ctx context.Context) {
			c := v.Client()
			for _, r := range c.DoMulti(ctx, c.B().Incr().Key("n").Build(), c.B().Incr().Key("n").Build()) {
				if e := r.Error(); e != nil {
					t.Error(e)
				}
			}
			done = true
		})
		s.Spawn("reader", func(ctx context.Context) {
			c := v.Client()
			for !done {
				n, e := c.Do(ctx, c.B().Get().Key("n").Build()).AsInt64()
				if e != nil && !valkey.IsValkeyNil(e) {
					t.Error(e)
				}
				if e == nil && n == 1 {
					witnessed = true
				}
			}
		})
		err := s.Run(time.Second)
		s.Close()
		if err != nil {
			t.Error(err)
		}
		if witnessed {
			break
		}
	}
	if !witnessed {
		t.Error("no read could observe the pipeline between its two commands")
	}
}

func TestUnboundedHangIsActiveWork(t *testing.T) {
	s := qenv.New(&qenv.Script{Faults: []qenv.Scripted{{Kind: "valkey.db.request", Name: "hang"}}})
	defer s.Close()
	v := New(s, "db")
	returned := false
	s.Spawn("caller", func(ctx context.Context) { c := v.Client(); c.Do(ctx, c.B().Get().Key("k").Build()); returned = true })
	err := s.Run(time.Second)
	if !errors.Is(err, qenv.ErrStuck) || returned {
		t.Errorf("unbounded hang: returned=%v err=%v", returned, err)
	}
}

func TestVirtualContextPreservesParentCause(t *testing.T) {
	run(t, qenv.Nominal{}, func(ctx context.Context, v *Server, c *Client) {
		parent, stop := context.WithCancelCause(ctx)
		timed, cancel := WithTimeout(parent, time.Hour)
		defer cancel()
		cause := errors.New("owner stopped")
		stop(cause)
		if !errors.Is(timed.Err(), context.Canceled) || !errors.Is(context.Cause(timed), cause) {
			t.Errorf("Err=%v Cause=%v", timed.Err(), context.Cause(timed))
		}
	})
}
