package ciconfig

import (
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// A dispatch of the nightly may take part of it (docs/ci.md, "Targeted
// nightly runs"), and a schedule takes all of it, as before the filters
// existed. These tests evaluate the workflow's own expressions (a job's
// if, run-name) with GitHub's semantics, against what ci/nightly-select.sh
// prints for the same inputs, so they judge what a run would do, not how
// its text reads.

type dispatchWorkflow struct {
	RunName string `yaml:"run-name"`
	On      struct {
		Schedule []map[string]string `yaml:"schedule"`
		Dispatch struct {
			Inputs map[string]struct {
				Type    string `yaml:"type"`
				Default string `yaml:"default"`
			} `yaml:"inputs"`
		} `yaml:"workflow_dispatch"`
	} `yaml:"on"`
	Jobs map[string]struct {
		If       string    `yaml:"if"`
		Needs    yaml.Node `yaml:"needs"`
		Timeout  any       `yaml:"timeout-minutes"`
		Strategy struct {
			FailFast *bool     `yaml:"fail-fast"`
			Matrix   yaml.Node `yaml:"matrix"`
		} `yaml:"strategy"`
		Steps []struct {
			Name string            `yaml:"name"`
			Run  string            `yaml:"run"`
			Env  map[string]string `yaml:"env"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

func readDispatch(t *testing.T) dispatchWorkflow {
	t.Helper()
	b, err := os.ReadFile("../../.github/workflows/nightly.yml")
	if err != nil {
		t.Fatal(err)
	}
	var w dispatchWorkflow
	if err := yaml.Unmarshal(b, &w); err != nil {
		t.Fatal(err)
	}
	return w
}

// selectPlan runs ci/nightly-select.sh plan as the select job does.
func selectPlan(t *testing.T, event string, inputs map[string]string) (map[string]string, error) {
	t.Helper()
	cmd := exec.Command("bash", "../../ci/nightly-select.sh", "plan")
	cmd.Env = append(os.Environ(), "EVENT="+event, "JOBS="+inputs["jobs"], "MUTANTS="+inputs["mutants"], "BINDINGS="+inputs["bindings"], "ALONE_GROUPS="+inputs["groups"])
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("%v: %s", err, ee.Stderr)
		}
		return nil, err
	}
	outputs := map[string]string{}
	for line := range strings.Lines(string(out)) {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			t.Fatalf("select plan printed %q, not key=value", line)
		}
		outputs[k] = v
	}
	return outputs, nil
}

// selected evaluates every job's if for one run: the jobs that would run.
func selected(t *testing.T, w dispatchWorkflow, event string, inputs map[string]string) []string {
	t.Helper()
	outputs, err := selectPlan(t, event, inputs)
	if err != nil {
		t.Fatalf("select fails for %s %v: %v", event, inputs, err)
	}
	ctx := ghContext(event, inputs, outputs)
	var run []string
	for id, job := range w.Jobs {
		if id == "select" {
			continue
		}
		if job.If == "" || truthy(evalGH(t, job.If, ctx)) {
			run = append(run, id)
		}
	}
	slices.Sort(run)
	return run
}

func ghContext(event string, inputs map[string]string, outputs map[string]string) map[string]any {
	ctx := map[string]any{"github.event_name": event}
	if event == "workflow_dispatch" {
		for _, k := range []string{"jobs", "mutants", "bindings", "groups"} {
			ctx["inputs."+k] = inputs[k] // a dispatch's inputs are strings, "" by default
		}
	} // a schedule has no inputs: they evaluate to null
	for k, v := range outputs {
		ctx["needs.select.outputs."+k] = v
	}
	return ctx
}

func needsList(n yaml.Node) []string {
	switch n.Kind {
	case yaml.ScalarNode:
		return []string{n.Value}
	case yaml.SequenceNode:
		var l []string
		for _, c := range n.Content {
			l = append(l, c.Value)
		}
		return l
	}
	return nil
}

func nightlyIDs(t *testing.T) []string {
	t.Helper()
	out, err := exec.Command("bash", "../../ci/nightly-select.sh", "ids").Output()
	if err != nil {
		t.Fatal(err)
	}
	ids := strings.Fields(string(out))
	slices.Sort(ids)
	return ids
}

// Every job is gated by the jobs filter: a schedule and an unfiltered
// dispatch run all of them, a dispatch naming jobs runs those alone, and a
// name that is no job fails select instead of running nothing.
func TestNightlyDispatchSelectsJobs(t *testing.T) {
	w := readDispatch(t)
	if len(w.On.Schedule) == 0 {
		t.Fatal("the nightly lost its schedule")
	}
	for _, name := range []string{"jobs", "mutants", "bindings", "groups"} {
		in, ok := w.On.Dispatch.Inputs[name]
		if !ok || in.Type != "string" || in.Default != "" {
			t.Fatalf("workflow_dispatch input %s must be a string defaulting to empty (every job, every row): %+v", name, in)
		}
	}
	ids := nightlyIDs(t)
	var jobs []string
	for id, job := range w.Jobs {
		if id == "select" {
			continue
		}
		jobs = append(jobs, id)
		if !slices.Contains(needsList(job.Needs), "select") {
			t.Errorf("job %s does not need select: its if cannot read the selection", id)
		}
		if job.If == "" {
			t.Errorf("job %s has no if: a filtered dispatch would run it anyway", id)
		}
	}
	slices.Sort(jobs)
	if !slices.Equal(ids, jobs) {
		t.Fatalf("ci/nightly-select.sh's ids %v differ from the workflow's jobs %v", ids, jobs)
	}

	for _, run := range []struct {
		event  string
		inputs map[string]string
	}{
		{"schedule", nil},
		// a schedule ignores whatever its environment holds
		{"schedule", map[string]string{"jobs": "fuzz", "mutants": "qenv"}},
		{"workflow_dispatch", map[string]string{}},
		{"workflow_dispatch", map[string]string{"mutants": "qenv"}},
	} {
		if got := selected(t, w, run.event, run.inputs); !slices.Equal(got, ids) {
			t.Errorf("%s with %v runs %v, not every job %v", run.event, run.inputs, got, ids)
		}
	}
	// a job selected takes the jobs it needs with it, and only those
	implied := map[string][]string{"verify-alone": {"verify-alone", "verify-alone-plan"}}
	for _, id := range ids {
		want := []string{id}
		if l, ok := implied[id]; ok {
			want = l
		}
		if got := selected(t, w, "workflow_dispatch", map[string]string{"jobs": id}); !slices.Equal(got, want) {
			t.Errorf("dispatch of jobs=%s runs %v, not %v", id, got, want)
		}
		for _, need := range needsList(w.Jobs[id].Needs) {
			if need != "select" && !slices.Contains(want, need) {
				t.Errorf("jobs=%s would skip %s, which it needs", id, need)
			}
		}
	}
	two := []string{"benchmarks", "core-mutants"}
	for _, sep := range []string{",", " ", ", "} {
		if got := selected(t, w, "workflow_dispatch", map[string]string{"jobs": strings.Join(two, sep)}); !slices.Equal(got, two) {
			t.Errorf("dispatch of jobs=%q runs %v", strings.Join(two, sep), got)
		}
	}
	for _, bad := range []map[string]string{
		{"jobs": "core-mutant"},
		{"jobs": "*"},
		{"jobs": "fuzz", "mutants": "qenv"},
		{"jobs": "fuzz", "bindings": "hello"},
		{"mutants": "nosuchpkg"},
		{"bindings": "no/such/binding"},
		// a verify-alone binding is chosen by its group, not in verify
		{"jobs": "verify", "bindings": "examples/wallet-grist/wallet.binding.yaml"},
		{"jobs": "verify", "groups": "wallet"},
		{"groups": "no-such-group"},
		{"jobs": "verify-alone", "groups": "*"},
	} {
		if _, err := selectPlan(t, "workflow_dispatch", bad); err == nil {
			t.Errorf("select accepted %v: it would run less than asked and show green", bad)
		}
	}
}

type mutantRow struct {
	Pkg string `json:"pkg"`
	Max int    `json:"max"`
}

// core-mutants' rows are ci/core-mutants.txt's, every one unless the
// mutants filter keeps some, each with its own max: a filter removes rows
// and never changes a limit.
func TestNightlyDispatchFiltersMutantsRows(t *testing.T) {
	w := readDispatch(t)
	job := w.Jobs["core-mutants"]
	if job.Strategy.FailFast == nil || *job.Strategy.FailFast {
		t.Fatal("core-mutants must keep fail-fast: false, so every package reports")
	}
	if job.Strategy.Matrix.Value != "${{ fromJSON(needs.select.outputs.mutants) }}" {
		t.Fatalf("core-mutants' matrix must be select's rows: %q", job.Strategy.Matrix.Value)
	}
	if job.Timeout != 120 {
		t.Fatalf("core-mutants' limit changed: %v", job.Timeout)
	}
	b, err := os.ReadFile("../../ci/core-mutants.txt")
	if err != nil {
		t.Fatal(err)
	}
	var file []mutantRow
	for line := range strings.Lines(string(b)) {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		n, err := strconv.Atoi(f[len(f)-1])
		if len(f) != 2 || err != nil || n < 0 {
			t.Fatalf("ci/core-mutants.txt: %q is not a package and its max", line)
		}
		file = append(file, mutantRow{f[0], n})
	}
	if len(file) == 0 {
		t.Fatal("ci/core-mutants.txt has no rows")
	}
	rows := func(event string, inputs map[string]string) []mutantRow {
		t.Helper()
		outputs, err := selectPlan(t, event, inputs)
		if err != nil {
			t.Fatal(err)
		}
		var m struct {
			Include []map[string]any `json:"include"`
		}
		if err := json.Unmarshal([]byte(outputs["mutants"]), &m); err != nil {
			t.Fatalf("mutants output %q: %v", outputs["mutants"], err)
		}
		var l []mutantRow
		for _, r := range m.Include {
			if len(r) != 2 {
				t.Fatalf("a row carries more than its package and max: %v", r)
			}
			max, ok := r["max"].(float64)
			if !ok {
				t.Fatalf("row %v: max is not a number", r)
			}
			l = append(l, mutantRow{r["pkg"].(string), int(max)})
		}
		return l
	}
	for _, event := range []string{"schedule", "workflow_dispatch"} {
		if got := rows(event, nil); !slices.Equal(got, file) {
			t.Errorf("%s's rows %v, not ci/core-mutants.txt's %v", event, got, file)
		}
	}
	for _, r := range file {
		for _, inputs := range []map[string]string{
			{"mutants": r.Pkg},
			{"jobs": "core-mutants", "mutants": r.Pkg},
		} {
			if got := rows("workflow_dispatch", inputs); !slices.Equal(got, []mutantRow{r}) {
				t.Errorf("mutants=%s keeps %v, not its row %v", r.Pkg, got, r)
			}
		}
	}
	both := []mutantRow{file[0], file[len(file)-1]}
	if got := rows("workflow_dispatch", map[string]string{"mutants": both[1].Pkg + "," + both[0].Pkg}); !slices.Equal(got, both) {
		t.Errorf("two packages keep %v, not %v", got, both)
	}
}

// verify's bindings filter only narrows the roster; the deadline stays
// the job's (TestNightlyVerifyRosterSharesTheUnchangedDeadline).
func TestNightlyDispatchFiltersVerifyBindings(t *testing.T) {
	w := readDispatch(t)
	var step string
	for _, s := range w.Jobs["verify"].Steps {
		if strings.Contains(s.Run, " simulate ") {
			step = s.Run
			if s.Env["BINDINGS"] != "${{ inputs.bindings }}" {
				t.Fatalf("verify's roster step must read the bindings input: %v", s.Env)
			}
		}
	}
	list := `mapfile -t bindings < <(ci/verify-roster.sh shared)`
	filter := `ci/nightly-select.sh bindings)`
	if i, j := strings.Index(step, list), strings.Index(step, filter); i < 0 || j < i || j > strings.Index(step, "left=") {
		t.Fatalf("verify must filter its listed bindings before the deadline is computed:\n%s", step)
	}
	out, err := exec.Command("../../ci/verify-roster.sh", "shared").Output()
	if err != nil {
		t.Fatal(err)
	}
	tracked := strings.Fields(string(out)) // verify's roster
	filtered := func(bindings string) ([]string, error) {
		cmd := exec.Command("bash", "../../ci/nightly-select.sh", "bindings")
		cmd.Env = append(os.Environ(), "BINDINGS="+bindings)
		cmd.Stdin = strings.NewReader(strings.Join(tracked, "\n") + "\n")
		out, err := cmd.Output()
		return strings.Fields(string(out)), err
	}
	if got, err := filtered(""); err != nil || !slices.Equal(got, tracked) {
		t.Fatalf("an empty filter must keep every binding: %v %v", got, err)
	}
	got, err := filtered("examples/hello/, bank.binding")
	if err != nil || len(got) == 0 || len(got) >= len(tracked) {
		t.Fatalf("filter kept %v (%v)", got, err)
	}
	for _, b := range got {
		if !slices.Contains(tracked, b) || !(strings.Contains(b, "examples/hello/") || strings.Contains(b, "bank.binding")) {
			t.Fatalf("filter kept %s, which it does not name", b)
		}
	}
	if _, err := filtered("no/such/binding"); err == nil {
		t.Fatal("a term naming no binding must fail, not verify nothing")
	}
}

type aloneRow struct {
	Name     string `json:"name"`
	Minutes  int    `json:"minutes"`
	Bindings string `json:"bindings"`
}

// verify-alone's rows are its plan's, every group of ci/verify-alone.txt
// unless a dispatch's groups filter keeps some, each as it is: a filter
// removes rows and never changes a group's minutes or bindings.
func TestNightlyDispatchFiltersVerifyAloneGroups(t *testing.T) {
	w := readDispatch(t)
	var plan string
	for _, s := range w.Jobs["verify-alone-plan"].Steps {
		if strings.Contains(s.Run, "ci/verify-roster.sh alone") {
			plan = s.Run
			if s.Env["ALONE_GROUPS"] != "${{ inputs.groups }}" {
				t.Fatalf("verify-alone-plan must read the groups input: %v", s.Env)
			}
		}
	}
	if !strings.Contains(plan, "matrix=$(ci/verify-roster.sh alone)\n") {
		t.Fatalf("verify-alone-plan must fail when the roster does (an assignment, not an echo of a substitution):\n%s", plan)
	}
	job := w.Jobs["verify-alone"]
	if job.Strategy.FailFast == nil || *job.Strategy.FailFast || job.Timeout != "${{ matrix.minutes }}" {
		t.Fatal("verify-alone must keep fail-fast: false and each group's own minutes")
	}
	rows := func(groups string) ([]aloneRow, error) {
		t.Helper()
		cmd := exec.Command("bash", "-e", "-c", plan)
		cmd.Dir = "../.."
		cmd.Env = append(os.Environ(), "ALONE_GROUPS="+groups, "GITHUB_OUTPUT="+t.TempDir()+"/out")
		out, err := cmd.Output()
		if err != nil {
			return nil, err
		}
		var m struct{ Include []aloneRow }
		if err := json.Unmarshal([]byte(strings.TrimPrefix(strings.TrimSpace(string(out)), "matrix=")), &m); err != nil {
			t.Fatalf("plan printed %q: %v", out, err)
		}
		return m.Include, nil
	}
	all, err := rows("")
	if err != nil || len(all) < 2 {
		t.Fatalf("every group: %v %v", all, err)
	}
	for _, g := range all {
		if got, err := rows(g.Name); err != nil || !slices.Equal(got, []aloneRow{g}) {
			t.Errorf("groups=%s keeps %v (%v), not its row %v", g.Name, got, err, g)
		}
	}
	two := []aloneRow{all[0], all[len(all)-1]}
	if got, err := rows(two[1].Name + ", " + two[0].Name); err != nil || !slices.Equal(got, two) {
		t.Errorf("two groups keep %v (%v), not %v", got, err, two)
	}
	if _, err := rows("no-such-group"); err == nil {
		t.Error("a name of no group must fail the plan, not run no group")
	}
	// a schedule has no inputs.groups; select ignores a stray value
	if outputs, err := selectPlan(t, "schedule", map[string]string{"groups": "wallet"}); err != nil || outputs["jobs"] == "" {
		t.Errorf("a schedule with a stray groups value: %v %v", outputs, err)
	}
}

// A filtered dispatch is named partial; a schedule or an unfiltered
// dispatch is "nightly". J7c counts scheduled full runs only, and a
// partial run must never read as one.
func TestNightlyDispatchRunNameMarksPartialRuns(t *testing.T) {
	w := readDispatch(t)
	name := func(event string, inputs map[string]string) string {
		v := evalGH(t, w.RunName, ghContext(event, inputs, nil))
		s, ok := v.(string)
		if !ok {
			t.Fatalf("run-name evaluates to %v", v)
		}
		return s
	}
	if got := name("schedule", nil); got != "nightly" {
		t.Fatalf("a scheduled run is named %q", got)
	}
	if got := name("workflow_dispatch", map[string]string{}); got != "nightly" {
		t.Fatalf("an unfiltered dispatch is named %q", got)
	}
	inputs := slices.Sorted(maps.Keys(w.On.Dispatch.Inputs))
	for _, in := range inputs {
		got := name("workflow_dispatch", map[string]string{in: "qenv"})
		if !strings.HasPrefix(got, "nightly (partial: ") || !strings.Contains(got, "qenv") {
			t.Errorf("a dispatch filtering by %s is named %q: it must read as partial and say what it took", in, got)
		}
	}
	if got := name("workflow_dispatch", map[string]string{"jobs": "core-mutants", "mutants": "qenv"}); got != "nightly (partial: core-mutants, mutants qenv)" {
		t.Errorf("jobs=core-mutants mutants=qenv is named %q", got)
	}
}

// The expression evaluator's own cases, GitHub's documented semantics.
func TestGHExpressions(t *testing.T) {
	ctx := map[string]any{"inputs.a": "", "x": `["a","B"]`}
	for expr, want := range map[string]any{
		"inputs.a == ''":                     true,
		"inputs.missing == ''":               true, // null == '' (both 0)
		"inputs.missing != ''":               false,
		"'A' == 'a'":                         true,
		"inputs.a || 'd'":                    "d",
		"'x' && 'y'":                         "y",
		"!(inputs.a != '')":                  true,
		"contains(fromJSON(x), 'b')":         true,
		"contains(fromJSON(x), 'c')":         false,
		"contains('abc', 'B')":               true,
		"format('{0}-{1}', 'p', 'q')":        "p-q",
		"${{ format('{{0}}{0}', 'z') }}":     "{0}z",
		"'it''s'":                            "it's",
		"inputs.a != '' && 'yes' || 'no'":    "no",
		"(inputs.a == '' || false) && 'yes'": "yes",
	} {
		if got := evalGH(t, expr, ctx); got != want {
			t.Errorf("%s = %#v, want %#v", expr, got, want)
		}
	}
}

// evalGH evaluates a GitHub Actions expression (with or without ${{ }})
// over the subset the nightly uses: literals, context paths, ! == != && ||,
// parentheses, format, contains and fromJSON. A path missing from ctx is
// null.
func evalGH(t *testing.T, expr string, ctx map[string]any) any {
	t.Helper()
	s := strings.TrimSpace(expr)
	if strings.HasPrefix(s, "${{") && strings.HasSuffix(s, "}}") {
		s = s[3 : len(s)-2]
	}
	p := &ghParser{src: s, ctx: ctx}
	v := p.or()
	p.space()
	if p.err == nil && p.pos != len(p.src) {
		p.err = fmt.Errorf("unexpected %q", p.src[p.pos:])
	}
	if p.err != nil {
		t.Fatalf("expression %q: %v", expr, p.err)
	}
	return v
}

type ghParser struct {
	src string
	pos int
	ctx map[string]any
	err error
}

func (p *ghParser) space() {
	for p.pos < len(p.src) && strings.ContainsRune(" \t\n", rune(p.src[p.pos])) {
		p.pos++
	}
}

func (p *ghParser) eat(tok string) bool {
	p.space()
	if strings.HasPrefix(p.src[p.pos:], tok) {
		p.pos += len(tok)
		return true
	}
	return false
}

func (p *ghParser) fail(format string, args ...any) any {
	if p.err == nil {
		p.err = fmt.Errorf(format, args...)
	}
	p.pos = len(p.src)
	return nil
}

func (p *ghParser) or() any {
	v := p.and()
	for p.err == nil && p.eat("||") {
		r := p.and()
		if !truthy(v) {
			v = r
		}
	}
	return v
}

func (p *ghParser) and() any {
	v := p.cmp()
	for p.err == nil && p.eat("&&") {
		r := p.cmp()
		if truthy(v) {
			v = r
		}
	}
	return v
}

func (p *ghParser) cmp() any {
	v := p.unary()
	for p.err == nil {
		switch {
		case p.eat("=="):
			v = ghEqual(v, p.unary())
		case p.eat("!="):
			v = !ghEqual(v, p.unary())
		default:
			return v
		}
	}
	return v
}

func (p *ghParser) unary() any {
	if p.eat("!") {
		return !truthy(p.unary())
	}
	return p.primary()
}

func (p *ghParser) primary() any {
	p.space()
	if p.eat("(") {
		v := p.or()
		if !p.eat(")") {
			return p.fail("missing )")
		}
		return v
	}
	if p.pos < len(p.src) && p.src[p.pos] == '\'' {
		var b strings.Builder
		for p.pos++; p.pos < len(p.src); p.pos++ {
			if p.src[p.pos] == '\'' {
				if p.pos+1 < len(p.src) && p.src[p.pos+1] == '\'' {
					b.WriteByte('\'')
					p.pos++
					continue
				}
				p.pos++
				return b.String()
			}
			b.WriteByte(p.src[p.pos])
		}
		return p.fail("unterminated string")
	}
	start := p.pos
	for p.pos < len(p.src) && (strings.ContainsRune("._-", rune(p.src[p.pos])) ||
		'a' <= p.src[p.pos] && p.src[p.pos] <= 'z' || 'A' <= p.src[p.pos] && p.src[p.pos] <= 'Z' || '0' <= p.src[p.pos] && p.src[p.pos] <= '9') {
		p.pos++
	}
	word := p.src[start:p.pos]
	switch word {
	case "":
		return p.fail("expected a value at %q", p.src[p.pos:])
	case "true":
		return true
	case "false":
		return false
	case "null":
		return nil
	}
	if !p.eat("(") {
		if n, err := strconv.ParseFloat(word, 64); err == nil {
			return n
		}
		return p.ctx[word]
	}
	var args []any
	for !p.eat(")") {
		if len(args) > 0 && !p.eat(",") {
			return p.fail("expected , in %s()", word)
		}
		args = append(args, p.or())
		if p.err != nil {
			return nil
		}
	}
	switch word {
	case "format":
		if len(args) == 0 {
			return p.fail("format needs a string")
		}
		f, _ := args[0].(string)
		var b strings.Builder
		for i := 0; i < len(f); i++ {
			switch {
			case strings.HasPrefix(f[i:], "{{"):
				b.WriteByte('{')
				i++
			case strings.HasPrefix(f[i:], "}}"):
				b.WriteByte('}')
				i++
			case f[i] == '{':
				j := strings.IndexByte(f[i:], '}')
				if j < 0 {
					return p.fail("format %q: unclosed placeholder", f)
				}
				n, err := strconv.Atoi(f[i+1 : i+j])
				if err != nil || n+1 >= len(args) {
					return p.fail("format %q: bad placeholder", f)
				}
				b.WriteString(ghString(args[n+1]))
				i += j
			default:
				b.WriteByte(f[i])
			}
		}
		return b.String()
	case "contains":
		if len(args) != 2 {
			return p.fail("contains takes two arguments")
		}
		if l, ok := args[0].([]any); ok {
			return slices.ContainsFunc(l, func(e any) bool { return ghEqual(e, args[1]) })
		}
		return strings.Contains(strings.ToLower(ghString(args[0])), strings.ToLower(ghString(args[1])))
	case "fromJSON":
		var v any
		if len(args) != 1 || json.Unmarshal([]byte(ghString(args[0])), &v) != nil {
			return p.fail("fromJSON(%v): not JSON", args)
		}
		return v
	}
	return p.fail("unknown function %s", word)
}

func truthy(v any) bool {
	switch v := v.(type) {
	case nil:
		return false
	case bool:
		return v
	case string:
		return v != ""
	case float64:
		return v != 0 && !math.IsNaN(v)
	}
	return true
}

func ghString(v any) string {
	switch v := v.(type) {
	case nil:
		return ""
	case string:
		return v
	case bool:
		return strconv.FormatBool(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// ghEqual: strings compare ignoring case; other types of a kind compare as
// values; mixed primitives compare as numbers (null and the empty string are both 0).
func ghEqual(a, b any) bool {
	if as, ok := a.(string); ok {
		if bs, ok := b.(string); ok {
			return strings.EqualFold(as, bs)
		}
	}
	num := func(v any) float64 {
		switch v := v.(type) {
		case nil:
			return 0
		case bool:
			if v {
				return 1
			}
			return 0
		case float64:
			return v
		case string:
			if strings.TrimSpace(v) == "" {
				return 0
			}
			if n, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
				return n
			}
		}
		return math.NaN()
	}
	switch a.(type) {
	case []any, map[string]any:
		return false // objects compare by identity: never equal here
	}
	return num(a) == num(b)
}
