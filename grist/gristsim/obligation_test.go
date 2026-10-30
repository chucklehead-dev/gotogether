package gristsim_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/grist/internal/gen"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/gristsim"
	"github.com/chucklehead-dev/gotogether/grist/simstore"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qtest"
	"github.com/chucklehead-dev/gotogether/validate"
)

type posted struct {
	Topic string `json:"topic" grist:"tag"`
}

type post struct {
	Topic string `json:"topic"`
}

type counted struct {
	Topic string `json:"topic" grist:"tag"`
}

type letter struct {
	Topic string `json:"topic"`
}

type delivered struct{}

// board is an App with a processor of each obligation's kind: count
// (Emits), mail (an at-least-once effect calling the post office's Keyed
// send) and archive (at most once). stall names the one whose handler
// never returns. Unlike waiting for appends, a hang holds a trigger and
// prevents simulation settlement.
func board(stall string) *grist.App {
	return boardWaiting(stall, 0)
}

// boardWaiting either stalls the named handler or lets it finish after delay.
// The independent catalog/model is unchanged by the handler's timing.
func boardWaiting(stall string, delay time.Duration) *grist.App {
	a := grist.New("board")
	ev := grist.NewEvent[posted](a, "posted")
	cnt := grist.NewEvent[counted](a, "counted")
	grist.NewCommand(a, "post", func(r *grist.Reads, c post) ([]grist.Event, error) {
		return []grist.Event{ev.Of(posted(c))}, nil
	}, grist.Appends(ev), grist.Authorize(grist.Authenticated))
	office := grist.NewDependency(a, "office")
	send := grist.NewCall[letter, delivered](office, "send", grist.Keyed())
	hang := func(ctx context.Context, name string) {
		if stall == name {
			if delay > 0 {
				qenv.Sleep(ctx, delay)
			} else {
				qenv.Wait(ctx, func() bool { return false })
			}
		}
	}
	grist.NewProcessor(a, "count", ev, func(ctx context.Context, r *grist.Reads, e posted, m grist.Meta) grist.Result {
		hang(ctx, "count")
		return grist.Emit(cnt.Of(counted(e)))
	}, grist.Emits(cnt))
	grist.NewProcessor(a, "mail", ev, func(ctx context.Context, r *grist.Reads, e posted, m grist.Meta) grist.Result {
		hang(ctx, "mail")
		return grist.Effect(func(ctx context.Context) error {
			_, err := send.Do(ctx, letter(e))
			return err
		}, grist.AtLeastOnce)
	}, grist.Delivers(grist.AtLeastOnce), grist.Uses(send))
	grist.NewProcessor(a, "archive", ev, func(ctx context.Context, r *grist.Reads, e posted, m grist.Meta) grist.Result {
		hang(ctx, "archive")
		return grist.Effect(func(context.Context) error { return nil }, grist.AtMostOnce)
	}, grist.Delivers(grist.AtMostOnce))
	return a
}

const boardRules = `module boardRules {
  import grist.* from "./grist"
  import boardEvents.* from "./board_gen"
  pure val STRS: Set[str] = Set("a", "b")
  pure val INTS: Set[int] = 1.to(2)
  pure def postOutcome(log: List[Event], c: PostArgs): str = "ok"
  pure def postEvents(log: List[Event], c: PostArgs): List[Event] = [posted(c.topic)]
  pure def postNeeds(c: PostArgs, result: str): Set[Read] = Set()
  pure def countEmits(prefix: List[Event], trigger: int): List[Event] = [counted(postedTopic(prefix[trigger - 1]))]
  pure def mailSkips(prefix: List[Event], trigger: int): bool = false
  pure def archiveSkips(prefix: List[Event], trigger: int): bool = false
  pure def mailOfficeSend(prefix: List[Event], trigger: int): OfficeSendRequest = { topic: postedTopic(prefix[trigger - 1]) }
}
`

// boardModel has no vals of its own: its default binding judges the
// generated obligations alone.
const boardModel = `module board {
  import grist.* from "./grist"
  import boardEvents.* from "./board_gen"
  import boardRules.* from "./board_rules"
  import boardGen.* from "./board_steps"
  export boardGen.*
}

module boardCalls {
  import grist.* from "./grist"
  import boardEvents.* from "./board_gen"
  import boardRules.* from "./board_rules"
  import boardCallsGen.* from "./board_steps"
  export boardCallsGen.*
}
`

// boardBinding writes the board's model for app and returns its store
// binding's path.
func boardBinding(t *testing.T, app *grist.App) string {
	t.Helper()
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "model"), 0o755)
	for f, src := range map[string]string{"board_rules.qnt": boardRules, "board.qnt": boardModel} {
		if err := os.WriteFile(filepath.Join(dir, "model", f), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	g, err := gen.Generate(app, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Missing) > 0 {
		t.Fatalf("holes missing: %v", g.Missing)
	}
	if err := g.Write(dir); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "board.binding.yaml")
}

// unrecorded answers every call ok without applying it: no served record,
// as a dependency (or a stub in front of it) that answers a key it never
// applied.
type unrecorded struct{}

func (unrecorded) Call(ctx context.Context, c grist.CallInfo, call string, req json.RawMessage) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}

// checkpointUnavailable leaves the mail worker unable to establish whether it
// has work. The retry backoff is active, unlike a successful empty-feed read.
type checkpointUnavailable struct{ *simstore.Store }

func (s checkpointUnavailable) Checkpoint(ctx context.Context, tenant, processor string) (grist.Position, error) {
	if processor == "mail" {
		return 0, errors.New("checkpoint store unavailable")
	}
	return s.Store.Checkpoint(ctx, tenant, processor)
}

// The obligations the catalog implies are judged at the run's settle: the
// faithful board conforms and settles; a processor whose handler never
// returns remains Unsettled, with no completion verdict; and a dependency
// answering a Keyed call it never applied violates the call's obligation
// after the worker finishes. These are distinct completion scopes.
func TestObligations(t *testing.T) {
	qtest.RequireQuint(t)
	cfg := gristsim.Config{Tenants: []string{"t1"}, Clients: 1, Ops: 3}
	faithful := board("")
	// every board has one catalog (a stall is in a handler), so one model
	// judges them all: its quint compile, cached by the files' paths, is
	// made once, not once per case
	binding := boardBinding(t, faithful)
	// a stalled run is unsettled at every seed: one is enough
	explore := func(t *testing.T, app *grist.App, cfg gristsim.Config, seeds int) *qtest.Exploration {
		return qtest.System{Binding: binding, Rates: qenv.NoFaults,
			Run: func(ch qenv.Chooser) error { return gristsim.Run(ch, app, cfg) }}.
			Explore(t, qtest.ExploreConfig{Seeds: seeds})
	}
	t.Run("faithful", func(t *testing.T) {
		t.Parallel()
		explore(t, faithful, cfg, 2).Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()))
	})
	t.Run("unstarted-workers", func(t *testing.T) {
		t.Parallel()
		// A command can commit while no processors are deployed. That is a
		// genuinely quiet runtime with unfulfilled business obligations;
		// generated eventual checks must still reject it at settlement.
		sys := qtest.System{Binding: binding, Rates: qenv.NoFaults, Run: func(ch qenv.Chooser) error {
			s := qenv.New(ch)
			defer s.Close()
			st := simstore.New(qenv.NewStore(s, "board", qenv.Serializable))
			var workErr error
			s.Spawn("command", func(ctx context.Context) {
				if workErr = st.CreateTenant(ctx, "t1"); workErr != nil {
					return
				}
				workErr = gristsim.Do(ctx, faithful, st, grist.Envelope{ID: "post-1", Tenant: "t1", Caller: "client"}, "post", json.RawMessage(`{"topic":"a"}`))
				if workErr != nil {
					return
				}
				r, err := grist.NewRelay(st)
				if err != nil {
					workErr = err
					return
				}
				workErr = r.Flush(ctx)
			})
			if err := s.Run(time.Second); err != nil {
				return err
			}
			if workErr != nil {
				return workErr
			}
			return s.Settle(time.Second)
		}}
		sys.Explore(t, qtest.ExploreConfig{Seeds: 1}).Expect(t, qtest.All(qtest.Settles()),
			qtest.All(qtest.Violates(validate.EventuallyName("countEventuallyHandled"))),
			qtest.All(qtest.Violates(validate.EventuallyName("mailEventuallyHandled"))),
			qtest.All(qtest.Violates(validate.EventuallyName("archiveEventuallyHandled"))))
	})
	t.Run("checkpoint-backoff", func(t *testing.T) {
		t.Parallel()
		sys := qtest.System{Binding: binding, Rates: qenv.NoFaults, Run: func(ch qenv.Chooser) error {
			s := qenv.New(ch)
			defer s.Close()
			st := checkpointUnavailable{simstore.New(qenv.NewStore(s, "board", qenv.Serializable))}
			var workErr error
			s.Spawn("setup", func(ctx context.Context) {
				if workErr = st.CreateTenant(ctx, "t1"); workErr != nil {
					return
				}
				workErr = gristsim.Do(ctx, faithful, st, grist.Envelope{ID: "post-1", Tenant: "t1", Caller: "client"}, "post", json.RawMessage(`{"topic":"a"}`))
				if workErr == nil {
					workErr = faithful.Start(ctx, st)
				}
			})
			err := s.Settle(time.Second)
			if workErr != nil {
				return workErr
			}
			return err
		}}
		e := sys.Explore(t, qtest.ExploreConfig{Seeds: 1})
		e.Expect(t, qtest.None(qtest.Settles()))
		assertWorkerUnsettled(t, e, "grist.mail.t1")
	})
	for _, stall := range []string{"count", "mail", "archive"} {
		t.Run("stall-"+stall, func(t *testing.T) {
			if testing.Short() && stall != "mail" {
				t.Skip("full mode: the other shapes' obligations")
			}
			t.Parallel()
			e := explore(t, board(stall), cfg, 1)
			e.Expect(t, qtest.None(qtest.Settles()))
			assertWorkerUnsettled(t, e, "grist."+stall+".t1")
		})
	}
	t.Run("unserved", func(t *testing.T) {
		t.Parallel()
		app := faithful
		office := app.Dependencies()[0]
		unserved := cfg
		unserved.Setup = func(*qenv.Sim, grist.Store) func(context.Context) context.Context {
			return func(ctx context.Context) context.Context { return office.With(ctx, unrecorded{}) }
		}
		explore(t, app, unserved, 1).Expect(t, qtest.All(qtest.Violates(validate.EventuallyName("officeSendEventuallyServed"))),
			qtest.None(qtest.Violates(validate.EventuallyName("mailEventuallyHandled"))))
	})
}

func assertWorkerUnsettled(t *testing.T, e *qtest.Exploration, worker string) {
	t.Helper()
	for _, r := range e.Runs {
		if r.Err != nil || !strings.Contains(r.Unsettled, worker) {
			t.Fatalf("worker did not remain pending: err=%v unsettled=%q", r.Err, r.Unsettled)
		}
		for _, s := range r.Recorded {
			if s.Settled {
				t.Fatal("unfinished handler acquired a settle marker")
			}
		}
		for _, a := range r.Report.Actors {
			if a.Settled {
				t.Fatalf("unfinished handler acquired a completion verdict: %s", a.Summary())
			}
			for _, ab := range a.Absent {
				if ab.Kind == "eventually" && ab.Cause == validate.Defect {
					t.Fatalf("unfinished handler classified as completed obligation defect: %+v", ab)
				}
			}
		}
	}
}

func TestWorkerCompletionBounds(t *testing.T) {
	qtest.RequireQuint(t)
	binding := boardBinding(t, board(""))
	for _, sql := range []bool{false, true} {
		for _, delay := range []time.Duration{10 * time.Second, 2 * time.Minute} {
			t.Run(fmt.Sprintf("sql=%t/delay=%s", sql, delay), func(t *testing.T) {
				t.Parallel()
				cfg := gristsim.Config{Tenants: []string{"t1"}, Clients: 1, Ops: 1, Horizon: time.Millisecond, SQL: sql, Send: []string{"post"}}
				e := gristsim.System(binding, boardWaiting("mail", delay), cfg).WithRates(qenv.NoFaults).
					Explore(t, qtest.ExploreConfig{Seeds: 1})
				if delay < time.Minute {
					e.Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()))
				} else {
					e.Expect(t, qtest.None(qtest.Settles()))
					assertWorkerUnsettled(t, e, "grist.mail.t1")
				}
			})
		}
	}
}
