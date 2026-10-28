package main

import (
	"flag"
	"fmt"
	"os"
	"reflect"
	"runtime/debug"
	"strings"

	"github.com/chucklehead-dev/gotogether/internal/nearest"
)

// A command's help: what it is for and a first example, above its flags;
// short is its line in gotogether help, the question it answers.
type command struct{ purpose, example, short string }

// helpOrder is the order gotogether help lists the commands in: a
// newcomer's first ones first. Read-only.
var helpOrder = []string{"scaffold", "lint", "simulate", "validate", "nav", "evidence", "steps", "shrink", "itf", "convert", "cover", "skills", "doctor"}

var commands = map[string]command{
	"scaffold": {"write a binding skeleton for a model (or, with -system, a qtest.System test for a binding): the first step",
		"gotogether scaffold -spec model/stock.qnt -module stock -out stock.binding.yaml\n  gotogether scaffold -system -binding stock.binding.yaml -out stock_test.go",
		"where do I start? a binding skeleton for a model; -system, a qtest.System test for a binding"},
	"simulate": {"run the model alone with the binding's invariants: which it guarantees (simulate.holds), which it lets fail (simulate.allows), and what it must be able to do (simulate.possible)",
		"gotogether simulate -binding stock.binding.yaml -graph\n  gotogether simulate -binding stock.binding.yaml   (sampled by quint, for a model too big to enumerate)",
		"what does the model alone allow? invariants held or broken, scenarios possible; -graph: every reachable state, a proof for a small model"},
	"evidence": {"sum up a suite's explorations (written under GOTOGETHER_EVIDENCE): each test's verdicts, and what no test tried",
		"GOTOGETHER_EVIDENCE=ev go test ./... && gotogether evidence ev",
		"what did a suite's explorations try, and what did no test try?"},
	"lint": {"check a binding against its model, the code's Record calls against the binding, or code meant for simulation or weaving",
		"gotogether lint -binding stock.binding.yaml -src .",
		"does the binding fit the model, and the code's Record calls the binding? -sim, -weave: code meant for simulation or weaving"},
	"steps": {"list recorded steps, or select and sanitize them into a step log",
		"gotogether steps -actors node1 -out node1.jsonl runs/*.jsonl",
		"which steps were recorded? list them, or select and sanitize them into a step log"},
	"validate": {"check recorded runs (step logs, OTLP/JSON, ITF) against the model",
		"gotogether validate -binding stock.binding.yaml runs/*.jsonl",
		"do recorded runs (step logs, OTLP/JSON, ITF) conform to the model? -replay judges a manifest's run again"},
	"shrink": {"shrink a failing sequence of an external driver's actions to a 1-minimal one that fails the same way, rerunning the driver per candidate and judging its steps",
		"gotogether shrink -binding webjobs.binding.yaml -actions journey.json -out shrunk -- ./play -actions {actions} -steps {steps}",
		"which of an external driver's failing actions does the failure need?"},
	"nav": {"navigate a model's states (its branches, and its disabled actions with why) or an exploration's run by its choices, one node at a time (-json for agents; on a terminal, gotogether-nav when installed)",
		"gotogether nav -binding stock.binding.yaml -follow 'reserve(\"a\", 1)'\n  gotogether nav -run 'TestShop#2:seed=17' -at fail -json",
		"what could the model do here, and why not that? a model's states, or an exploration's run by its choices, a node at a time"},
	"itf": {"turn recorded steps into a model-level ITF trace per actor", "gotogether itf -binding stock.binding.yaml -out itf/ runs/*.jsonl",
		"the model-level ITF trace of recorded steps, per actor"},
	"convert": {"turn an app's OTLP/JSON spans into a step log with rules", "gotogether convert -rules spans.convert.yaml -out steps.jsonl spans.json",
		"an app's OTLP/JSON spans as a step log, by rules"},
	"cover": {"compare a real environment's code coverage with a simulation's: what ran only for real, only simulated",
		"gotogether cover -covdir covdata -sim ev/app/TestX/x1.json",
		"what code ran only for real, only simulated?"},
	"skills": {"list the agent skills (skills list, the default), or install them into a project (skills install; its -h)",
		"gotogether skills install -target all",
		"the agent skills: list them, or install them into a project"},
	"skills install": {"install the agent skills into a project (.agents/skills and .claude/skills by default)", "gotogether skills install",
		"install the agent skills into a project"},
	"doctor": {"report the installed pieces' versions (the CLI and its core, the module's core and published modules, qweave's woven ABI, gotogether-nav, binaries named, quint, its evaluator, Node) and the formats this version reads, naming each skew with its fix",
		"gotogether doctor\n  gotogether doctor -C ./service -strict ./bin/service",
		"do the installed pieces fit together? every version, and each skew with its fix"},
}

// commandFlags is the flag set of a command, whose -h says what the
// command is for, gives an example, and lists its flags, removed ones
// (whose usage starts "gone:") left out.
func commandFlags(name string) *flag.FlagSet {
	c, ok := commands[name]
	if !ok {
		panic("gotogether: command " + name + " has no help: add it to commands")
	}
	fset := flag.NewFlagSet(name, flag.ExitOnError)
	fset.Usage = func() {
		w := fset.Output()
		fmt.Fprintf(w, "gotogether %s: %s\n\n  %s\n\nflags:\n", name, c.purpose, c.example)
		fset.VisitAll(func(f *flag.Flag) {
			if strings.HasPrefix(f.Usage, "gone:") {
				return
			}
			def := ""
			if !isZeroValue(f) && !strings.Contains(f.Usage, "(default") {
				def = fmt.Sprintf(" (default %s)", f.DefValue)
			}
			fmt.Fprintf(w, "  -%s\n    \t%s%s\n", f.Name, f.Usage, def)
		})
	}
	return fset
}

// isZeroValue reports whether the flag's default is its type's zero
// value ("", false, 0, 0s), which a usage leaves unsaid: an int flag's
// "(default 0)" once followed the default its usage stated. As the flag
// package's own check: a new zero Value of the flag's type, printed.
func isZeroValue(f *flag.Flag) bool {
	t := reflect.TypeOf(f.Value)
	var z reflect.Value
	if t.Kind() == reflect.Pointer {
		z = reflect.New(t.Elem())
	} else {
		z = reflect.Zero(t)
	}
	v, ok := z.Interface().(flag.Value)
	if !ok {
		return f.DefValue == ""
	}
	return f.DefValue == v.String()
}

// commandNames are main's commands, for the unknown command's
// did-you-mean.
var commandNames = []string{"scaffold", "simulate", "evidence", "lint", "steps", "validate", "shrink", "nav", "itf", "convert", "cover", "skills", "doctor", "help", "version"}

// unknownCommand says that name is no command, with the nearest one if
// it is a likely misspelling, and where the list is: the whole usage
// printed alone did not say what was wrong.
func unknownCommand(name string) string {
	msg := fmt.Sprintf("unknown command %q", name)
	if best := nearest.Name(name, commandNames); best != "" {
		msg += fmt.Sprintf(" (did you mean %s?)", best)
	}
	return msg + "; gotogether help lists the commands"
}

// needs is the error of a command run without what it needs: what to
// give, and its example (validate with no file once printed the whole
// usage, which did not say what was missing).
func needs(name, what string) error {
	ex, _, _ := strings.Cut(commands[name].example, "\n")
	return fmt.Errorf("%s needs %s: %s (gotogether %s -h lists its flags)", name, what, ex, name)
}

// version is the module's version as built (go install …@v), else its
// commit.
func version() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	v := bi.Main.Version
	for _, s := range bi.Settings {
		if s.Key == "vcs.revision" && (v == "" || v == "(devel)") {
			v = "commit " + s.Value
		}
	}
	return v
}

// help prints the usage to standard output: asked for, it is no error.
func help() {
	usageTo(os.Stdout)
	os.Exit(0)
}
