package gristsim_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/gristsim"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qtest"
)

type quietNoted struct {
	Topic string `json:"topic" grist:"tag"`
	Quiet bool   `json:"quiet"`
}

type quietNote struct {
	Topic string `json:"topic"`
	Quiet bool   `json:"quiet"`
}

// mailer is an App whose Delivers processor skips quiet notes: its
// handler returns Skip for them, an Effect for the rest.
func mailer() *grist.App {
	a := grist.New("mailer")
	ev := grist.NewEvent[quietNoted](a, "noted")
	grist.NewCommand(a, "note", func(r *grist.Reads, c quietNote) ([]grist.Event, error) {
		return []grist.Event{ev.Of(quietNoted(c))}, nil
	}, grist.Authorize(grist.Authenticated))
	grist.NewProcessor(a, "mail", ev, func(ctx context.Context, r *grist.Reads, e quietNoted, m grist.Meta) grist.Result {
		if e.Quiet {
			return grist.Skip()
		}
		return grist.Effect(func(context.Context) error { return nil }, grist.AtLeastOnce)
	}, grist.Delivers(grist.AtLeastOnce))
	return a
}

// mailerRules are the mailer's rules (model/mailer_rules.qnt), skips the
// mailSkips hole.
func mailerRules(skips string) string {
	return `module mailerRules {
  import grist.* from "./grist"
  import mailerEvents.* from "./mailer_gen"
  pure val STRS: Set[str] = Set("a", "b")
  pure val INTS: Set[int] = 1.to(2)
  pure def noteOutcome(log: List[Event], c: NoteArgs): str = "ok"
  pure def noteEvents(log: List[Event], c: NoteArgs): List[Event] = [noted(c.topic, c.quiet)]
  pure def noteNeeds(c: NoteArgs, result: str): Set[Read] = Set()
  pure def mailSkips(prefix: List[Event], trigger: int): bool = ` + skips + `
}
`
}

// mailerModel is the mailer's model/mailer.qnt, the bindings' modules.
const mailerModel = `module mailer {
  import grist.* from "./grist"
  import mailerEvents.* from "./mailer_gen"
  import mailerRules.* from "./mailer_rules"
  import mailerGen.* from "./mailer_steps"
  export mailerGen.*
}

module mailerCalls {
  import grist.* from "./grist"
  import mailerEvents.* from "./mailer_gen"
  import mailerRules.* from "./mailer_rules"
  import mailerCallsGen.* from "./mailer_steps"
  export mailerCallsGen.*
}
`

// A Delivers processor's Skip is its skip step (mailSkip), which the
// generated model takes where the app's rule mailSkips says so: the
// faithful rule conforms, a rule that skips nothing rejects the skip,
// and one that skips everything rejects the effect (in full mode).
func TestDeliversSkip(t *testing.T) {
	qtest.RequireQuint(t)
	a := mailer()
	for _, c := range []struct {
		skips string
		want  []qtest.Claim
	}{
		{"notedQuiet(prefix[trigger - 1])", []qtest.Claim{qtest.Conforms(), qtest.Takes("mailSkip"), qtest.Takes("mailEffect")}},
		{"false", []qtest.Claim{qtest.Some(qtest.Rejects("mailSkip"))}},
		{"true", []qtest.Claim{qtest.Some(qtest.Rejects("mailEffect"))}},
	} {
		if testing.Short() && c.skips != "notedQuiet(prefix[trigger - 1])" {
			continue // the contrasts in full mode
		}
		dir := t.TempDir()
		os.MkdirAll(filepath.Join(dir, "model"), 0o755)
		if err := os.WriteFile(filepath.Join(dir, "model", "mailer_rules.qnt"), []byte(mailerRules(c.skips)), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "model", "mailer.qnt"), []byte(mailerModel), 0o644); err != nil {
			t.Fatal(err)
		}
		g, err := grist.Generate(a, dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(g.Missing) > 0 {
			t.Fatalf("holes missing: %v", g.Missing)
		}
		if err := g.Write(dir); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(g.Files["model/mailer_steps.qnt"]), "action mailSkip(trigger: int)") {
			t.Fatalf("no skip action in the skeleton")
		}
		cfg := gristsim.Config{Tenants: []string{"t1"}, Clients: 2, Ops: 3}
		qtest.System{Binding: filepath.Join(dir, "mailer.binding.yaml"), Rates: qenv.NoFaults,
			Run: func(ch qenv.Chooser) error { return gristsim.Run(ch, a, cfg) }}.
			Explore(t, qtest.ExploreConfig{Seeds: 3}).Expect(t, c.want...)
	}
}
