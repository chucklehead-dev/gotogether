package gristsim_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/gristsim"
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
// never returns, the poller's handling dropped: a hang the scheduler
// cannot tell from a poller waiting for appends (the poller is a
// daemon), so the run settles with its triggers unhandled.
func board(stall string) *grist.App {
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
			qenv.Wait(ctx, func() bool { return false })
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
	g, err := grist.Generate(app, dir)
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

// The obligations the catalog implies are judged at the run's settle: the
// faithful board conforms and settles; a processor whose handler never
// returns (its trigger never handled, its poller waiting as it does for
// appends) violates its own obligation, whichever shape it has; and a
// dependency answering a Keyed call it never applied violates the
// call's. Without the generated vals every stalled run would conform:
// nothing else in the model says a trigger must be handled.
func TestObligations(t *testing.T) {
	qtest.RequireQuint(t)
	cfg := gristsim.Config{Tenants: []string{"t1"}, Clients: 1, Ops: 3}
	// a stalled run violates at every seed: one is enough
	explore := func(t *testing.T, app *grist.App, cfg gristsim.Config, seeds int) *qtest.Exploration {
		return qtest.System{Binding: boardBinding(t, app), Rates: qenv.NoFaults,
			Run: func(ch qenv.Chooser) error { return gristsim.Run(ch, app, cfg) }}.
			Explore(t, qtest.ExploreConfig{Seeds: seeds})
	}
	faithful := board("")
	t.Run("faithful", func(t *testing.T) {
		t.Parallel()
		explore(t, faithful, cfg, 2).Expect(t, qtest.Conforms(), qtest.All(qtest.Settles()))
	})
	for _, stall := range []string{"count", "mail", "archive"} {
		t.Run("stall-"+stall, func(t *testing.T) {
			if testing.Short() && stall != "mail" {
				t.Skip("full mode: the other shapes' obligations")
			}
			t.Parallel()
			explore(t, board(stall), cfg, 1).Expect(t, qtest.All(qtest.Settles()),
				qtest.All(qtest.Violates(validate.EventuallyName(stall+"EventuallyHandled"))))
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
