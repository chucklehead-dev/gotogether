package main

import (
	"flag"
	"fmt"
	"os"
	"reflect"
	"runtime/debug"
	"strings"
)

// A command's help: what it is for and a first example, above its flags.
type command struct{ purpose, example string }

var commands = map[string]command{
	"scaffold": {"write a binding skeleton for a model (or, with -system, a qtest.System test for a binding): the first step",
		"gotogether scaffold -spec model/stock.qnt -module stock -out stock.binding.yaml\n  gotogether scaffold -system -binding stock.binding.yaml -out stock_test.go"},
	"simulate": {"run the model alone with the binding's invariants: which it guarantees (simulate.holds), which it lets fail (simulate.allows)",
		"gotogether simulate -binding stock.binding.yaml"},
	"evidence": {"sum up a suite's explorations (written under GOTOGETHER_EVIDENCE): each test's verdicts, and what no test tried",
		"GOTOGETHER_EVIDENCE=ev go test ./... && gotogether evidence ev"},
	"lint": {"check a binding against its model, the code's Record calls against the binding, or code meant for simulation or weaving",
		"gotogether lint -binding stock.binding.yaml -src ."},
	"steps": {"list recorded steps, or select and sanitize them into a step log",
		"gotogether steps -actors node1 -out node1.jsonl runs/*.jsonl"},
	"validate": {"check recorded runs (step logs, OTLP/JSON, ITF) against the model",
		"gotogether validate -binding stock.binding.yaml runs/*.jsonl"},
	"itf":     {"turn recorded steps into a model-level ITF trace per actor", "gotogether itf -binding stock.binding.yaml -out itf/ runs/*.jsonl"},
	"convert": {"turn an app's OTLP/JSON spans into a step log with rules", "gotogether convert -rules spans.convert.yaml -out steps.jsonl spans.json"},
	"cover": {"compare a real environment's code coverage with a simulation's: what ran only for real, only simulated",
		"gotogether cover -covdir covdata -sim ev/app/TestX/x1.json"},
	"skills": {"list the agent skills (skills list, the default), or install them into a project (skills install; its -h)",
		"gotogether skills install -target all"},
	"skills install": {"install the agent skills into a project (.agents/skills and .claude/skills by default)", "gotogether skills install"},
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
var commandNames = []string{"scaffold", "simulate", "evidence", "lint", "steps", "validate", "itf", "convert", "cover", "skills", "help", "version"}

// unknownCommand says that name is no command, with the nearest one if
// it is a likely misspelling, and where the list is: the whole usage
// printed alone did not say what was wrong.
func unknownCommand(name string) string {
	best, bestD := "", -1
	for _, c := range commandNames {
		if d := editDistance(strings.ToLower(name), c); bestD < 0 || d < bestD {
			best, bestD = c, d
		}
	}
	msg := fmt.Sprintf("unknown command %q", name)
	if bestD >= 0 && bestD <= max(2, len(name)/3) {
		msg += fmt.Sprintf(" (did you mean %s?)", best)
	}
	return msg + "; gotogether help lists the commands"
}

// editDistance is the Levenshtein distance between a and b.
func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
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
