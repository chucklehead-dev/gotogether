package skills

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestSkillsHaveFrontMatter(t *testing.T) {
	list, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) < 7 {
		t.Fatalf("%d skills", len(list))
	}
	for _, s := range list {
		if len(s.Description) < 40 {
			t.Errorf("%s: description too short: %q", s.Name, s.Description)
		}
		b, _ := os.ReadFile(filepath.Join(s.Name, "SKILL.md"))
		if frontMatter(string(b), "name") != s.Name {
			t.Errorf("%s: front matter name must equal the directory name", s.Name)
		}
	}
	dir := t.TempDir()
	paths, err := Install(dir, false)
	refs, _ := fs.Glob(FS(), "*/references/*.md")
	if err != nil || len(paths) != len(list)+len(refs) {
		t.Fatalf("%v %v", paths, err)
	}
	if _, err := Install(dir, false); err == nil {
		t.Fatal("overwrote without force")
	}
	dirs, err := TargetDirs("p", nil)
	if err != nil || len(dirs) != 2 || dirs[0] != filepath.Join("p", ".agents", "skills") {
		t.Fatalf("default targets: %v %v", dirs, err)
	}
	if _, err := TargetDirs("p", []string{"nope"}); err == nil {
		t.Fatal("unknown target accepted")
	}
}

// The repository exposes the skills where agents look for them.
func TestRepoSkillDirs(t *testing.T) {
	list, _ := List()
	for _, d := range []string{"../.agents/skills", "../.claude/skills"} {
		for _, s := range list {
			if _, err := os.Stat(filepath.Join(d, s.Name, "SKILL.md")); err != nil {
				t.Errorf("%s/%s: %v (symlink it to ../../skills/%s)", d, s.Name, err, s.Name)
			}
		}
	}
}

// Every relative link in a skill or reference resolves among the
// installed files: a skill loads its references (and another skill's) by
// path.
func TestSkillLinksResolve(t *testing.T) {
	link := regexp.MustCompile(`\]\(([^)#:]+\.md)\)`)
	_ = fs.WalkDir(FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, _ := fs.ReadFile(FS(), p)
		for _, m := range link.FindAllStringSubmatch(string(b), -1) {
			target := filepath.ToSlash(filepath.Clean(filepath.Join(filepath.Dir(p), m[1])))
			if strings.HasPrefix(target, "..") {
				continue // outside the skills (the repository's docs)
			}
			if _, err := fs.Stat(FS(), target); err != nil {
				t.Errorf("%s links to %s, which is not installed with the skills", p, m[1])
			}
		}
		return nil
	})
}

// Skills teach the current API: an identifier removed from the code (or
// a report format it no longer prints) would send agents to it.
func TestSkillsAvoidDeprecated(t *testing.T) {
	deprecated := []struct{ text, instead string }{
		{"qtest.Check(", "qtest.Validate(...).Expect"},
		{"qtest.RecordAndCheck", "qtest.Record(...).Expect"},
		{"qtest.Explore(", "qtest.System{...}.Explore"},
		{"qtest.SweepExplore", "System.Sweep"},
		{"qtest.Explored", "qtest.Run"},
		{"qtest.Expect(", "Run.Expect (qtest.Validate(...).Expect)"},
		{"`qtest.Expect`", "`.Expect(t, claims...)` (Run's, Exploration's)"},
		{"qtest.Expect,", ".Expect(t, claims...) (Run's, Exploration's)"},
		{"DurableFileSink", "qobs.FileSink(path, qobs.PerStep)"},
		{"BufferedFileSink", "qobs.FileSink(path, qobs.Buffered)"},
		{"SyncedFileSink", "qobs.FileSink(path, qobs.Synced)"},
		{"NewSyncedJSONLSink", "qobs.NewJSONLSink(w, qobs.Synced)"},
		{"rec, sink, err := qnemesis.NewRecorder", "rec, err := qnemesis.NewRecorder (rec.Close() closes the log)"},
		{"qobs.Strict", "internal/hooks.Strict (the core's own)"},
		{"qobs.Escaped", "internal/hooks.Escaped (the core's own)"},
		{"qenv.WaitUntil", "qenv.WaitFor (a duration)"},
		{"qenv.FaultOptions", "the coverage report's option names"},
		{"connect.FaultMap", "qtest.FaultMap"},
		{"qtest.Resolve", "qtest.CheckCases"},
		{"ExploreConfig{Seeds: 30, Rates:", "System.Rates, or sys.WithRates(r)"},
		{"qtest.Settings", "qtest.CheckCases' CaseCheck"},
		{"qhegel.CheckAll", "qhegel.Test (Mode After)"},
		{"qenv.KnownFault", "the workflows' own check of a fault name (internal)"},
		{"connect.FaultScript", "qtest.FaultScript"},
		{"connect.Fault{", "qtest.Fault{"},
		{"qenv.Recorder", "qenv.ChoiceLog"},
		{"qhegel.Recorder", "qhegel.Case"},
		{"qhegel.Chooser", "qhegel.Choices"},
		{"qhegel.Replay", "qhegel.VaryTraces"},
		{"qhegel.NewRecorder", "qhegel.NewCase"},
		{"qenv.Faults(", "qenv.DeclareFaults("},
		{"`qenv.Faults`", "`qenv.DeclareFaults`"},
		{"qload.System", "qload.Target"},
		{"Chain.Expect", "Chain.Mean"},
		{".Expect(t, true)", "a named claim (qload.SomeTrigger, qload.Recovered)"},
		{".Expect(t, false)", "a named claim (qload.NoTrigger, qload.StaysDegraded)"},
		{"x.Coverage()", "x.Evidence().Faults"},
		{"x.CodeCoverage()", "x.Evidence().Code"},
		{"step [", `"step N" (reports number steps from 1)`},
		{"Sweep.Expect", "SweepConfig.Expect"},
		{"ExploreConfig.Opts", "System.Opts"},
		{"ConfirmByCheck", "validate.Checker.Judge (it confirms)"},
		{"Checker.Search(", "validate.Checker.Judge"},
		{"LinearizeStates", "qtrace.Linearize (its check gives states)"},
		{"LinearizeProof", "qtrace.Linearize (Linearization.Proof)"},
		{"LinearizePending", "qtrace.Linearize (LinearizeOptions.Pending)"},
		{"qtest.DefaultLinearizeBudget", "validate.DefaultLinearizeBudget"},
		{"qtest.DefaultResolveBudget", "validate.DefaultResolveBudget"},
		{"Evidence().Search", "Evidence().Exhaust"},
		{"binding.RenderModule", "internal/replay (the validator's own)"},
		{"binding.RenderBatch", "internal/replay (the validator's own)"},
		{"binding.PlanWithin", "binding.Binding.Planner(m, whole).Plan(t)"},
		{"Recovery.Judge", "the Recovery Recovers or Assess returns"},
		{"CheckRuns", "Checker.CheckBatch"},
		{"-noitf", "nothing: no ITF is the default (-itf writes it)"},
		{"gotogether/load", "gotogether/input (input.Read)"},
		{"Exploration.Coverage", "Exploration.Evidence().Faults"},
		{"Exploration.CodeCoverage", "Exploration.Evidence().Code"},
		{"Recorder.Env", "qhegel.Case.Env"},
		{"Swept", "qtest.SweepResult"},
		{"GOTOGETHER_SEED=", "GOTOGETHER_RUN='TestX#1:seed=n'"},
		{"`GOTOGETHER_SEED`", "`GOTOGETHER_RUN`"},
		{"qtest.Complete()", "nothing: a search MaxRuns cuts short fails unless Bound.Partial"},
		{"GOTOGETHER_SCHEDULE", "GOTOGETHER_RUN='TestX#1:schedule=...'"},
		{"s.Partitions(", "s.Net().PartitionNemesis("},
		{"s.Partition(", "s.Net().Partition("},
		{"s.Heal(", "s.Net().Heal("},
		{"PartitionLength", "Network.CutLength"},
		{"q.Proc", "qenv.NewQueue(s, name, p)"},
		{"Queue.Proc", "qenv.NewQueue(s, name, p)"},
		{`"queue.ack"`, `"queue.<name>.ack" ("queue" covers them)`},
		{`"store.commit"`, `"store.<name>.commit"`},
		{`"disk.crash"`, `"disk.<name>.crash"`},
		// G6's trim before the tags
		{"validate.Command", "cli.Check (package validate/cli)"},
		{"validate.Replay", "cli.Replay (package validate/cli)"},
		{"validate.Judged", "cli.Judged (package validate/cli)"},
		{"validate.VerdictOf", "Report.Verdict"},
		{"JudgeStepMutants", "validate.Checker.MutateSteps"},
		{"Manifest.AddInput", "validate.NewManifest(validate.RunRecord{...})"},
		{"Checker.Manifest", "validate.NewManifest(validate.RunRecord{...})"},
		{"validate.ModuleVersion", "validate.CurrentTools"},
		{"validate.QuintVersion", "validate.CurrentTools"},
		{"WriteArtifacts", "qshrink.Result's JSON (shrink.json)"},
		{"qshrink.Yes", "qshrink.Reproduced"},
		{"qshrink.Unknown", "qshrink.Undecided"},
		{"-require-settle", "-require-finish (the old name is a deprecated alias)"},
		// grist slice 1's review
		{"grist.Query{", "grist.Selector{ (Reads.Select), or grist.Events(r, t, tags...)"},
		{"freshCause", "freshKey(answered, id) (grist.qnt: a refusal answers its key too)"},
		{"grist.StepFields", "nothing: grist records the steps (internal/rec)"},
		// grist slice 2: commits recorded from the step log, both sides in every run
		{"gristsim.AtClients", "nothing: every run records both sides; a binding's actors: picks one (gristsim.ClientsActor)"},
		{"gristsim.AtCommits", "nothing: every run records both sides; a binding's actors: picks one"},
		{"Batch.OnCommit", "the step log: Batch.Step, recorded by grist.Relay"},
		{"grist.Quiet", "nothing: every gristsim run records both sides (gristsim.ClientsActor)"},
		{"grist.PosKey", "nothing: a store's own key layout (internal/poskey)"},
		{"grist.CheckCondition", "store.CheckCondition (package grist/store)"},
		// G7: the driver adapters published
		{"bombadiltrace.", "package qdriver/bombadil (journey.Bombadil, journey.FromTrace in webjobs)"},
		{"[bombadiltrace]", "package qdriver/bombadil"},
		// the review of qsql, qdriver, toQuint and actors
		{"qenv.RangeScanner", "Txn.ScanRange (every Txn has it)"},
		{"qenv.ScanRange(", "tx.ScanRange(r), or qenv.ScanPrefix in an adapter's own Txn"},
		{"qdriver.Observed", "qdriver.Step (an Event)"},
		{"bombadil.ReadFile", "bombadil.LogFile(path, opts) and its Steps()"},
		{"bombadil.Read(", "bombadil.Log(r, opts) and its Steps()"},
		{"Binding.Checks(", "Binding.ChecksActor"},
		{"`Binding.Checks`", "`Binding.ChecksActor`"},
		// C2: Go's coverage reader out of the simulated qcover
		{"qcover.ReadGoCover", "gocover.Read (package qcover/gocover)"},
		{"qcover.ParseGoCover", "gocover.Parse (package qcover/gocover)"},
		{"qcover.Compare", "gocover.Compare (package qcover/gocover)"},
		{"qcover.GoProfile", "gocover.Profile (package qcover/gocover)"},
		// H5: what woven code calls, in package woven (the woven builds'
		// ABI, never called by hand), and storetest's checks through Checks
		{"qcover.Hit(", "nothing: woven code calls woven.Hit"},
		{"qcover.Register(", "nothing: woven code calls woven.RegisterBlocks"},
		{"qmut.Register(", "nothing: woven code calls woven.RegisterMutants"},
		{"qmut.On(", "nothing: woven code calls woven.MutantOn"},
		{"qmut.Cond(", "nothing: woven code calls woven.Cond"},
		{"qmut.Pick(", "nothing: woven code calls woven.Pick"},
		{"weave.MutexLock", "nothing: woven code calls package woven"},
		{"weave.RWLock", "nothing: woven code calls package woven"},
		{"weave.GroupGo", "nothing: woven code calls package woven"},
		{"qenv/internal/keyed", "internal/wovenrt"},
		{"storetest.DensePositions", "storetest.Checks() (a Check by its Name)"},
		{"storetest.TenantIsolation", "storetest.Checks() (a Check by its Name)"},
		{"storetest.ConcurrentAgainstBruteForce", "storetest.Checks() (a Check by its Name)"},
		// H4: one vocabulary (grist's actor is the caller: "actor" is a
		// recorder's; the overlay harness and the catalog's claims in the
		// core)
		{"grist.WithActor", "grist.WithCaller"},
		{"ActorOf", "grist.CallerOf"},
		{"grist.Actors(", "grist.Callers("},
		{"ActorFromHeader", "grist.CallerFromHeader"},
		{"-actor-header", "-caller-header"},
		{"ActorHeader", "CallerHeader (client.Client's, gristsim's)"},
		{"Reads.Actor", "Reads.Caller"},
		{"Envelope.Actor", "Envelope.Caller"},
		{"grist.Delivery", "grist.Guarantee"},
		{"gristtest.Exercised", "gristtest.Catalog(App).Exercised(except...) (qtest.Catalog)"},
		{"gristtest.Takes", "gristtest.Catalog(App).Takes, Catalog.Take(name)"},
		{"gristtest.Mutants", "qtest.Mutants (Generated: gristtest.RuleMutants(App))"},
		{"gristtest.Mutant()", "qtest.Mutant()"},
		{"gristtest.MutantEnv", "qtest.MutantEnv"},
		{"GRIST_MUTANT", "GOTOGETHER_MUTANT (qtest.MutantEnv)"},
		{"gristtest.Overlay", "qtest.Mutants.Overlay"},
		{"Overlay(t, m, t.TempDir())", "qtest.Mutants{Package: ...}.Overlay(t, name) (webjobs' and payd's helpers are gone)"},
		{"system(NoDedupe)", "testdata/mutants/noDedupe, TestMutant switching on qtest.Mutant() (hello's Mutation enum is gone)"},
		{"hello.Mutation", "testdata/mutants/noDedupe (qtest.Mutants)"},
		{"TestMutantIsCaught", "TestMutant and TestMutants (gotogether scaffold -system: qtest.Mutants)"},
		// the project's old names, spelled in pieces so a rename script
		// that rewrites them cannot rewrite these
		{"quint" + "go", "gotogether"},
		{"QUINT" + "GO_", "GOTOGETHER_"},
		{"quint" + "hegel", "qhegel"},
		{"quint-connect" + "-go", "gotogether"},
		{"casselc/" + "quint", "chucklehead-dev/gotogether"},
		{"QENV_" + "CHECK", "GOTOGETHER_CHECK"},
		{"QUINT_" + "STEPS_DIR", "GOTOGETHER_STEPS_DIR"},
	}
	for old, now := range Renamed {
		deprecated = append(deprecated, struct{ text, instead string }{old + "`", now + "`"},
			struct{ text, instead string }{old + "/", now + "/"})
	}
	check := func(p string, b []byte) {
		for _, dep := range deprecated {
			if mentions(string(b), dep.text) {
				t.Errorf("%s mentions %s, which is gone: use %s", p, dep.text, dep.instead)
			}
		}
		// the free function: qtest.ExploreConfig is fine
		if freeExplore.Match(b) {
			t.Errorf("%s mentions qtest.Explore, which is gone: use System.Explore (qtest.System{...}.Explore)", p)
		}
	}
	_ = fs.WalkDir(FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".md") {
			return err
		}
		b, _ := fs.ReadFile(FS(), p)
		check(p, b)
		return nil
	})
	// the docs a user reads first; DESIGN.md and HISTORY.md are records,
	// in the names of their time
	docs, _ := filepath.Glob("../examples/*/README.md")
	agents, _ := filepath.Glob("../docs/agents/*.md") // AGENTS.md's pages by area
	docs = append(append(docs, agents...), "../README.md", "../AGENTS.md", "../qgosim/README.md", "../docs/instrumentation.md")
	for _, p := range docs {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		check(strings.TrimPrefix(p, "../"), b)
	}
}

var freeExplore = regexp.MustCompile(`qtest\.Explore\b[^C]`)

// Every new name is a skill, and an old install is removed only where its
// SKILL.md names it.
func TestRemoveRenamed(t *testing.T) {
	have := map[string]bool{}
	list, err := List()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range list {
		have[s.Name] = true
	}
	for old, now := range Renamed {
		if !have[now] {
			t.Errorf("Renamed[%q] = %q, which is not a skill", old, now)
		}
	}
	dir := t.TempDir()
	write := func(name, front string) {
		if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name, "SKILL.md"), []byte("---\nname: "+front+"\n---\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("quint-start", "quint-start")
	write("quint-hegel", "someone-elses")
	removed, err := RemoveRenamed(dir)
	if err != nil || len(removed) != 1 || filepath.Base(removed[0]) != "quint-start" {
		t.Fatalf("removed %v, %v; want quint-start alone", removed, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "quint-hegel")); err != nil {
		t.Error("a skill that only shares an old name was removed")
	}
}

// A code block marked <!-- from PATH --> copies PATH (from the repository's
// root): its lines appear there, in order ("// ..." stands for lines left
// out). A copy that drifted from the code it shows is a newcomer's red test.
func TestSnippetsMatchSource(t *testing.T) {
	docs, _ := filepath.Glob("../examples/*/README.md")
	docs = append(docs, "../README.md")
	_ = fs.WalkDir(FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".md") {
			docs = append(docs, p)
		}
		return err
	})
	marked := 0
	for _, doc := range docs {
		var b []byte
		var err error
		if strings.HasPrefix(doc, "../") {
			b, err = os.ReadFile(doc)
		} else {
			b, err = fs.ReadFile(FS(), doc)
		}
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range snippet.FindAllStringSubmatch(string(b), -1) {
			marked++
			src, err := os.ReadFile(filepath.Join("..", m[1]))
			if err != nil {
				t.Errorf("%s copies %s, which does not exist", doc, m[1])
				continue
			}
			var lines []string
			for _, l := range strings.Split(string(src), "\n") {
				lines = append(lines, strings.TrimSpace(l))
			}
			at := 0
			for _, l := range strings.Split(m[2], "\n") {
				l = strings.TrimSpace(l)
				if l == "" || l == "// ..." {
					continue
				}
				for at < len(lines) && lines[at] != l {
					at++
				}
				if at == len(lines) {
					t.Errorf("%s copies %s, but %q is not there (after the lines before it): copy it again", strings.TrimPrefix(doc, "../"), m[1], l)
					break
				}
				at++
			}
		}
	}
	if marked == 0 {
		t.Fatal("no marked snippets found: the marker's form changed")
	}
}

var snippet = regexp.MustCompile("(?s)<!-- from (\\S+) -->\\s*```go\\n(.*?)```")

// mentions reports whether text contains name as a whole name: an
// identifier ending it is not followed by more of one (qtest.Resolve is
// not in qtest.ResolvePending).
func mentions(text, name string) bool {
	for i := 0; ; {
		j := strings.Index(text[i:], name)
		if j < 0 {
			return false
		}
		end := i + j + len(name)
		last := name[len(name)-1]
		if !isIdent(last) || end == len(text) || !isIdent(text[end]) {
			return true
		}
		i = i + j + 1
	}
}

func isIdent(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}
