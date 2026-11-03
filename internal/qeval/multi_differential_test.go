package qeval

// This bounded extension checks joint state relations, never products of each
// variable's independently observed values. The pure relation is interpreted by
// both native Quint backends; sampled action witnesses are a second check, not
// an enumeration or completeness proof.

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/quintcmd"
	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/itf"
)

type multiCase struct {
	Name, Action, Then, Oracle string
	From                       [2]int
	SampleMayDisable           bool
}

func multiSame(a, b *Set) bool { return Compare(a, b) == 0 }

func multiAssignments(m *Model, name string, from [2]int) (values *Set, err error) {
	defer recoverInto(&err)
	d, ok := m.Decl(name)
	if !ok {
		return nil, fmt.Errorf("missing action %s", name)
	}
	ev := m.evaluation(nil, State{"x": Int(from[0]), "y": Int(from[1])})
	body, en, err := ev.bindArgs(d, nil, nil, name)
	if err != nil {
		return nil, err
	}
	next, err := ev.action(body, en)
	if err != nil {
		return nil, err
	}
	tuples := make([]Value, 0, len(next))
	for _, s := range next {
		x, xok := s.a["x"]
		y, yok := s.a["y"]
		if !xok || !yok || len(s.a) != 2 {
			return nil, fmt.Errorf("incomplete joint assignment %v", s.a)
		}
		tuples = append(tuples, Tup{x, y})
	}
	return NewSet(tuples), nil
}

func multiRelation(m *Model, i int, c multiCase) (*Set, error) {
	first, err := multiAssignments(m, fmt.Sprintf("e%d", i), c.From)
	if err != nil || c.Then == "" {
		return first, err
	}
	values := []Value{}
	for _, v := range first.Elems {
		pair := v.(Tup)
		next, err := multiAssignments(m, fmt.Sprintf("n%d", i), [2]int{int(pair[0].(Int)), int(pair[1].(Int))})
		if err != nil {
			return nil, err
		}
		values = append(values, next.Elems...)
	}
	return NewSet(values), nil
}

func multiModule(cases []multiCase) string {
	var b strings.Builder
	b.WriteString("module joint {\n var x: int\n var y: int\n var oracle: Set[(int, int)]\n")
	for i, c := range cases {
		fmt.Fprintf(&b, " action e%d = %s\n", i, c.Action)
		if c.Then != "" {
			fmt.Fprintf(&b, " action n%d = %s\n", i, c.Then)
		}
		fmt.Fprintf(&b, " val expected%d: Set[(int, int)] = %s\n", i, c.Oracle)
		fmt.Fprintf(&b, " run t%d = (all { x' = %d, y' = %d }).then(e%d)", 2*i, c.From[0], c.From[1], i)
		if c.Then != "" {
			fmt.Fprintf(&b, ".then(n%d)", i)
		}
		b.WriteByte('\n')
		fmt.Fprintf(&b, " run t%d = oracle' = expected%d\n", 2*i+1, i)
	}
	b.WriteString(" action init = all {x'=0,y'=0,oracle'=Set()}\n action step = true\n}\n")
	return b.String()
}

func multiCheck(t *testing.T, cases []multiCase, seed uint64) {
	t.Helper()
	skip.Quint(t)
	if len(cases) == 0 {
		t.Fatal("joint differential corpus is empty")
	}
	dir := keepDir(t, fmt.Sprintf("joint-seed-%d", seed))
	artifact, err := json.MarshalIndent(struct {
		Seed  uint64
		Cases []multiCase
	}{seed, cases}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cases.json"), artifact, 0644); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(dir, "joint.qnt")
	if err := os.WriteFile(source, []byte(multiModule(cases)), 0644); err != nil {
		t.Fatal(err)
	}
	irPath := filepath.Join(dir, "joint-ir.json")
	ir, err := os.Create(irPath)
	if err != nil {
		t.Fatal(err)
	}
	cmd := quintcmd.Command(t.Context(), "", dir, "compile", "--target", "json", "--main", "joint", source)
	var stderr strings.Builder
	cmd.Stdout, cmd.Stderr = ir, &stderr
	err = cmd.Run()
	closeErr := ir.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("seed %d compile: %v/%v\n%s\n%s", seed, err, closeErr, &stderr, multiModule(cases))
	}
	raw, err := os.ReadFile(irPath)
	if err != nil {
		t.Fatal(err)
	}
	m, err := Load(raw)
	if err != nil {
		t.Fatal(err)
	}
	actual := make([]*Set, len(cases))
	for i, c := range cases {
		actual[i], err = multiRelation(m, i, c)
		if err != nil {
			var u *Unsupported
			category := "error"
			if errors.As(err, &u) {
				category = "refused"
			}
			t.Fatalf("seed=%d case=%d %s %s: %v\nFrom=%v\nAction=%s\nThen=%s\nOracle=%s\nartifacts=%s", seed, i, c.Name, category, err, c.From, c.Action, c.Then, c.Oracle, dir)
		}
	}
	for _, backend := range backends {
		t.Run(backend, func(t *testing.T) {
			statuses := multiNative(t, dir, source, backend, seed, 2*len(cases))
			witnesses, disabled := 0, 0
			schema := &itf.Schema{Vars: map[string]*itf.Type{"x": {Kind: itf.Int}, "y": {Kind: itf.Int}, "oracle": {Kind: itf.Set, Elem: &itf.Type{Kind: itf.Tuple, Elems: []*itf.Type{{Kind: itf.Int}, {Kind: itf.Int}}}}}}
			for i, c := range cases {
				context := fmt.Sprintf("seed=%d case=%d %s backend=%s From=%v\nAction=%s\nThen=%s\nOracle=%s\nartifacts=%s", seed, i, c.Name, backend, c.From, c.Action, c.Then, c.Oracle, dir)
				oracleStatus := statuses[2*i+1]
				if oracleStatus.missing || oracleStatus.isFalse || oracleStatus.err != "" {
					t.Fatalf("%s\npure oracle %s", context, oracleStatus.String())
				}
				nativeOracle := multiITF(t, dir, backend, 2*i+1, schema)
				value, err := FromITF(nativeOracle.Vars["oracle"])
				if err != nil {
					t.Fatalf("%s: oracle transport: %v", context, err)
				}
				expected, ok := value.(*Set)
				if !ok {
					t.Fatalf("%s: oracle is %T", context, value)
				}
				if !multiSame(actual[i], expected) {
					t.Errorf("%s\njoint successors: Go %s; Quint oracle %s", context, String(actual[i]), String(expected))
					continue
				}
				witness := statuses[2*i]
				if witness.missing || witness.err != "" {
					t.Fatalf("%s\nnative action %s", context, witness.String())
				}
				if witness.isFalse {
					disabled++
					if len(expected.Elems) > 0 && !c.SampleMayDisable {
						t.Errorf("%s\nnative action disabled despite nonempty deterministic relation %s", context, String(expected))
					}
					continue // A disabled sampled draw provides no native state witness.
				}
				witnesses++
				nativeAction := multiITF(t, dir, backend, 2*i, schema)
				x, err := FromITF(nativeAction.Vars["x"])
				if err != nil {
					t.Fatal(err)
				}
				y, err := FromITF(nativeAction.Vars["y"])
				if err != nil {
					t.Fatal(err)
				}
				pair := Tup{x, y}
				found := false
				for _, v := range expected.Elems {
					found = found || Compare(pair, v) == 0
				}
				if !found {
					t.Errorf("%s\nnative sampled tuple %s not in exact joint oracle %s", context, String(pair), String(expected))
				}
			}
			t.Logf("seed %d: %d exact joint relation comparisons; %d native state witnesses; %d disabled sampled runs", seed, len(cases), witnesses, disabled)
		})
	}
}

func multiITF(t *testing.T, dir, backend string, index int, schema *itf.Schema) itf.State {
	t.Helper()
	path := filepath.Join(dir, backend, fmt.Sprintf("t%d.itf.json", index))
	tr, err := itf.ReadFileWith(path, schema)
	if err != nil || len(tr.States) == 0 {
		t.Fatalf("read native joint witness %s: %v", path, err)
	}
	return tr.States[len(tr.States)-1]
}

func multiNative(t *testing.T, dir, source, backend string, seed uint64, count int) []goutcome {
	t.Helper()
	outDir := filepath.Join(dir, backend)
	if err := os.MkdirAll(outDir, 0755); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(dir, backend+".out")
	out, err := os.Create(outPath)
	if err != nil {
		t.Fatal(err)
	}
	cmd := quintcmd.Command(t.Context(), "", dir, "test", "--main", "joint", "--backend", backend, "--match", "^t[0-9]+$", "--max-samples", "3", "--seed", fmt.Sprint(seed), "--out-itf", filepath.Join(outDir, "{test}.itf.json"), source)
	cmd.Stdout, cmd.Stderr = out, out
	runErr := cmd.Run()
	closeErr := out.Close()
	if t.Context().Err() != nil || closeErr != nil {
		t.Fatalf("joint native %s: %v/%v", backend, t.Context().Err(), closeErr)
	}
	text, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	result := make([]goutcome, count)
	for i := range result {
		result[i].missing = true
	}
	index := func(name string) int {
		i, err := strconv.Atoi(strings.TrimPrefix(name, "t"))
		if err != nil || i < 0 || i >= count {
			t.Fatalf("unexpected native run %q", name)
		}
		return i
	}
	current := -1
	for _, line := range strings.Split(string(text), "\n") {
		switch {
		case passedLine.MatchString(line):
			result[index(passedLine.FindStringSubmatch(line)[1])].missing = false
		case failedLine.MatchString(line):
			i := index(failedLine.FindStringSubmatch(line)[1])
			result[i].missing = false
			result[i].err = "failed"
		case detailLine.MatchString(line):
			current = index(detailLine.FindStringSubmatch(line)[1])
		case current >= 0 && errorLine.MatchString(line):
			match := errorLine.FindStringSubmatch(line)
			if match[1] == "QNT511" || match[1] == "QNT513" && strings.Contains(match[2], "Cannot continue in `then`") && strings.Contains(match[2], "evaluated to false") {
				result[current].err = ""
				result[current].isFalse = true
			} else {
				result[current].err = match[1] + " " + match[2]
			}
			current = -1
		}
	}
	if err := differentialNativeExit(runErr, result); err != nil {
		t.Fatalf("joint native %s: %v\n%s", backend, err, text)
	}
	return result
}

// A complete-looking report cannot turn a killed or broken tool into evidence.
// Exit 1 is Quint's ordinary failed-test exit, permitted only with a reported
// failure/disabled run. Per-run checks still reject unexpected runtime errors.
func differentialNativeExit(runErr error, results []goutcome) error {
	failed := false
	for _, result := range results {
		failed = failed || !result.missing && (result.err != "" || result.isFalse)
	}
	if runErr == nil {
		if failed {
			return fmt.Errorf("native tool exited successfully despite failed runs")
		}
		return nil
	}
	var exit *exec.ExitError
	if !errors.As(runErr, &exit) || exit.ExitCode() != 1 || !failed {
		return fmt.Errorf("native tool did not complete a reported failed test: %w", runErr)
	}
	return nil
}

func TestMultiVariableToolFailureIsNotAgreement(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("requires shell subprocesses for tool-exit controls")
	}
	passed := []goutcome{{}}
	disabled := []goutcome{{isFalse: true}}
	exit := func(script string) error { return exec.CommandContext(t.Context(), shell, "-c", script).Run() }
	if differentialNativeExit(nil, passed) != nil || differentialNativeExit(exit("exit 1"), disabled) != nil {
		t.Fatal("valid process outcomes rejected")
	}
	for _, bad := range []error{exit("exit 1"), exit("exit 2"), exit("kill -TERM $$"), errors.New("cannot start tool")} {
		if differentialNativeExit(bad, passed) == nil {
			t.Fatalf("tool failure became agreement: %v", bad)
		}
	}
	if differentialNativeExit(exit("exit 2"), disabled) == nil || differentialNativeExit(nil, disabled) == nil {
		t.Fatal("inconsistent process/report outcome became agreement")
	}
}

// multiTerm and multiAction describe a deliberately small finite language. The
// action renderer and pure-set denotation are separate: the latter substitutes
// old state and expands finite choices, leaving arithmetic/guards to Quint.
type multiTerm struct {
	name          string
	scale, offset int
}

func (e multiTerm) text(env map[string]string) string {
	base := "0"
	if e.name != "" {
		base = env[e.name]
	}
	return fmt.Sprintf("(%d * (%s) + (%d))", e.scale, base, e.offset)
}

type multiAction struct {
	kind, name string
	terms      [2]multiTerm
	children   []*multiAction
	domain     []int
	reverse    bool
}

func (a *multiAction) render(env map[string]string) string {
	switch a.kind {
	case "pair":
		first, second := "x' = "+a.terms[0].text(env), "y' = "+a.terms[1].text(env)
		if a.reverse {
			first, second = second, first
		}
		return "all {" + first + ", " + second + "}"
	case "any":
		return "any {" + a.children[0].render(env) + ", " + a.children[1].render(env) + "}"
	case "guard":
		return "all {" + a.terms[0].text(env) + " > " + a.terms[1].text(env) + ", " + a.children[0].render(env) + "}"
	case "if":
		return "if (" + a.terms[0].text(env) + " > " + a.terms[1].text(env) + ") " + a.children[0].render(env) + " else " + a.children[1].render(env)
	case "choice":
		local := map[string]string{}
		for k, v := range env {
			local[k] = v
		}
		local[a.name] = a.name
		domain := []string{}
		for _, v := range a.domain {
			domain = append(domain, strconv.Itoa(v))
		}
		return "{ nondet " + a.name + " = Set(" + strings.Join(domain, ",") + ").oneOf(); " + a.children[0].render(local) + " }"
	}
	panic("unknown joint action " + a.kind)
}
func (a *multiAction) denotation(env map[string]string) string {
	switch a.kind {
	case "pair":
		return "Set((" + a.terms[0].text(env) + ", " + a.terms[1].text(env) + "))"
	case "any":
		return "(" + a.children[0].denotation(env) + ").union(" + a.children[1].denotation(env) + ")"
	case "guard":
		return "if (" + a.terms[0].text(env) + " > " + a.terms[1].text(env) + ") " + a.children[0].denotation(env) + " else Set()"
	case "if":
		return "if (" + a.terms[0].text(env) + " > " + a.terms[1].text(env) + ") " + a.children[0].denotation(env) + " else " + a.children[1].denotation(env)
	case "choice":
		result := "Set()"
		for _, v := range a.domain {
			local := map[string]string{}
			for k, x := range env {
				local[k] = x
			}
			local[a.name] = strconv.Itoa(v)
			result = "(" + result + ").union(" + a.children[0].denotation(local) + ")"
		}
		return result
	}
	panic("unknown joint denotation " + a.kind)
}
func (a *multiAction) samplesMayDisable() bool {
	choice, guard := a.kind == "choice", a.kind == "guard"
	var scan func(*multiAction)
	scan = func(n *multiAction) {
		choice = choice || n.kind == "choice"
		guard = guard || n.kind == "guard"
		for _, c := range n.children {
			scan(c)
		}
	}
	scan(a)
	return choice && guard
}

func (a *multiAction) has(kind string) bool {
	if a.kind == kind {
		return true
	}
	for _, child := range a.children {
		if child.has(kind) {
			return true
		}
	}
	return false
}
func multiGenerate(seed uint64, count int) []multiCase {
	r := rand.New(rand.NewPCG(seed, seed^0x49c219ad))
	term := func(names []string) multiTerm {
		return multiTerm{name: names[r.IntN(len(names))], scale: r.IntN(5) - 2, offset: r.IntN(7) - 3}
	}
	var action func(int, []string) *multiAction
	action = func(depth int, names []string) *multiAction {
		terms := [2]multiTerm{term(names), term(names)}
		if depth == 0 {
			return &multiAction{kind: "pair", terms: terms, reverse: r.IntN(2) == 0}
		}
		switch r.IntN(5) {
		case 0:
			return &multiAction{kind: "any", children: []*multiAction{action(depth-1, names), action(depth-1, names)}}
		case 1:
			return &multiAction{kind: "guard", terms: terms, children: []*multiAction{action(depth-1, names)}}
		case 2:
			return &multiAction{kind: "if", terms: terms, children: []*multiAction{action(depth-1, names), action(depth-1, names)}}
		case 3:
			name := fmt.Sprintf("draw%d", len(names))
			return &multiAction{kind: "choice", name: name, domain: []int{-1, 0, 1}, children: []*multiAction{action(depth-1, append(append([]string{}, names...), name))}}
		default:
			return action(0, names)
		}
	}
	out := make([]multiCase, 0, count)
	for i := 0; i < count; i++ {
		from := [2]int{r.IntN(7) - 3, r.IntN(7) - 3}
		a := action(1+r.IntN(3), []string{"x", "y"})
		old := map[string]string{"x": strconv.Itoa(from[0]), "y": strconv.Itoa(from[1])}
		c := multiCase{Name: fmt.Sprintf("generated-%d", i), From: from, Action: a.render(map[string]string{"x": "x", "y": "y"}), Oracle: a.denotation(old), SampleMayDisable: a.samplesMayDisable()}
		if r.IntN(3) == 0 {
			next := action(1, []string{"x", "y"})
			c.Then = next.render(map[string]string{"x": "x", "y": "y"})
			c.Oracle = "(" + c.Oracle + ").map(jointState => " + next.denotation(map[string]string{"x": "jointState._1", "y": "jointState._2"}) + ").flatten()"
			// A sampled first step may reach a disabled continuation even when another
			// first successor has a continuation. The exact pure relation stays decisive.
			c.SampleMayDisable = c.SampleMayDisable || next.samplesMayDisable() || (a.has("choice") || a.has("any")) && next.has("guard")
		}
		out = append(out, c)
	}
	return out
}

func TestMultiVariableDifferentialAgainstQuint(t *testing.T) {
	skip.Quint(t)
	if testing.Short() {
		t.Skip("generated joint relations require compile plus both native backends")
	}
	batches := envNum(t, "GOTOGETHER_QEVAL_FUZZ", 2)
	size := envNum(t, "GOTOGETHER_QEVAL_MULTI_SIZE", 16)
	if size < 1 {
		t.Fatal("GOTOGETHER_QEVAL_MULTI_SIZE must be positive")
	}
	if batches == 0 {
		t.Skip("joint differential batches explicitly disabled")
	}
	offset := envNum(t, "GOTOGETHER_SEED_OFFSET", 0)
	seeds := make([]uint64, batches)
	for i := range seeds {
		seeds[i] = uint64(offset + i + 1)
	}
	if seed := envNum(t, "GOTOGETHER_QEVAL_FUZZ_SEED", 0); seed > 0 {
		seeds = []uint64{uint64(seed)}
	}
	for _, seed := range seeds {
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			t.Logf("reproduce: GOTOGETHER_QEVAL_FUZZ_SEED=%d GOTOGETHER_QEVAL_MULTI_SIZE=%d go test ./internal/qeval -run '^TestMultiVariableDifferentialAgainstQuint$' -count=1", seed, size)
			multiCheck(t, multiGenerate(seed, size), seed)
		})
	}
}
