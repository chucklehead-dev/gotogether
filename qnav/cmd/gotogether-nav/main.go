// Command gotogether-nav is the interactive navigator (DESIGN §58): a
// model's states, or an exploration's run by its choices, in the
// terminal. It takes `gotogether nav`'s arguments, which runs it on a
// terminal when it is installed:
//
//	gotogether-nav -binding b.yaml [-run steps.jsonl [-at k] [-actor a]] [-follow path] [-take t] [-fold]
//	gotogether-nav -run 'TestX#2:seed=17' [-pkg DIR] [-at fail|start|<schedule>@<k>] [-follow 1,0] [-take o] [-fold]
//
// A run is navigated through a session of its test binary
// (GOTOGETHER_NAV=serve): compiled once, each node a replay.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/chucklehead-dev/gotogether/input"
	"github.com/chucklehead-dev/gotogether/qnav/internal/session"
	"github.com/chucklehead-dev/gotogether/qnav/internal/tui"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "gotogether-nav:", err)
		os.Exit(2)
	}
}

func run(args []string) error {
	fset := flag.NewFlagSet("gotogether-nav", flag.ExitOnError)
	bind := fset.String("binding", "", "the binding whose model to navigate (from init, or with -run a step log: the state after its first -at calls)")
	runRef := fset.String("run", "", "a recorded run's step log (with -binding), or an exploration's run as a failing run's report names it ('TestX#2:seed=17', run in -pkg)")
	at := fset.String("at", "", "with a step log, how many of the actor's calls to take (default all); with an exploration's run, a node (start, fail, @<k>, <schedule>@<k>)")
	actor := fset.String("actor", "", "with a step log: the actor to navigate (default the only one)")
	follow := fset.String("follow", "", "a path from the node: a model's transitions (admit(\"a\"),release(\"a\")), a run's options (1,0,2)")
	take := fset.String("take", "", "one more branch to take after -follow")
	pkg := fset.String("pkg", ".", "with an exploration's run: the test's package directory")
	fold := fset.Bool("fold", false, "open the node folded (its linear stretch passed)")
	quint := fset.String("quint", "quint", "quint binary")
	asJSON := fset.Bool("json", false, "refused: gotogether nav -json is the agents' form")
	fset.Bool("save", false, "ignored: press s on a node to save it")
	fset.Bool("text", false, "ignored: gotogether nav -text prints a node as text")
	fset.Parse(args)
	if *asJSON {
		return errors.New("-json is gotogether nav's: run gotogether nav ... -json")
	}
	path := *follow
	if *take != "" {
		if path != "" {
			path += ","
		}
		path += *take
	}
	c := tui.Config{Fold: *fold, NoColor: os.Getenv("NO_COLOR") != ""}
	if *runRef != "" && isRunRef(*runRef) {
		if *bind != "" || *actor != "" {
			return fmt.Errorf("-run %s: an exploration's run is the test's, judged by its binding: drop -binding and -actor", *runRef)
		}
		for p := range strings.SplitSeq(path, ",") {
			if p = strings.TrimSpace(p); p == "" {
				continue
			}
			o, err := strconv.Atoi(p)
			if err != nil || o < 0 {
				return fmt.Errorf("-follow %q: a run's options, comma-separated (0 nominal)", path)
			}
			c.Follow = append(c.Follow, o)
		}
		fmt.Fprintf(os.Stderr, "gotogether-nav: compiling %s's test binary…\n", *runRef)
		s, err := session.Start(*pkg, *runRef)
		if err != nil {
			return err
		}
		defer s.Close()
		c.Run, c.Node, c.Title = s, *at, s.Info.Run
		return program(c)
	}
	if *bind == "" {
		return errors.New("give -binding (a model), with -run a step log, or -run an exploration's run ('TestX#2:seed=17')")
	}
	ch, err := validate.NewChecker(*bind, validate.Options{Quint: *quint})
	if err != nil {
		return err
	}
	defer ch.Close()
	start := validate.NavStart{Actor: *actor, At: -1}
	c.Title = "model " + ch.Binding.Module
	if *runRef != "" {
		f, err := input.Read(*runRef, input.Unknown)
		if err != nil {
			return err
		}
		start.Steps = f.Steps
		if start.Steps == nil {
			start.Steps = []qtrace.Step{}
		}
		if *at != "" {
			if start.At, err = strconv.Atoi(*at); err != nil {
				return fmt.Errorf("-at %q: with a step log, how many calls to take", *at)
			}
		}
		c.Failing = func() (tui.ModelNav, string, string, error) { return tui.Failing(ch, start) }
	} else if *at != "" || *actor != "" {
		return errors.New("-at and -actor open a recorded run: add -run steps.jsonl")
	}
	n, err := ch.Nav(start)
	var model tui.ModelNav = n
	if err != nil && start.Steps != nil && *at == "" {
		// all of a run the model rejects: open it at its failing step
		f, title, why, ferr := tui.Failing(ch, start)
		if ferr != nil {
			return ferr
		}
		model, c.Title, c.Why, err = f, c.Title+": "+title, why, nil
	}
	if err != nil {
		return err
	}
	c.Model, c.Path = model, validate.SplitPath(path)
	return program(c)
}

func program(c tui.Config) error {
	m := tui.New(c)
	if _, err := tea.NewProgram(m).Run(); err != nil {
		return err
	}
	if s := m.Status(); strings.HasPrefix(s, "error: ") {
		return errors.New(strings.TrimPrefix(s, "error: "))
	}
	return nil
}

// isRunRef reports whether s names an exploration's run (TestX#n:kind=v)
// rather than a file.
func isRunRef(s string) bool {
	if _, err := os.Stat(s); err == nil {
		return false
	}
	return reRunRef.MatchString(s)
}

var reRunRef = regexp.MustCompile(`#\d+:(seed|schedule|corpus|choices)=`)
