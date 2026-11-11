package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestShardSelectionPartitionsTheOrderedInventory(t *testing.T) {
	for total := range 13 {
		all := shardSpec{0, 1}.selection(total)
		if len(all) != total || !slices.IsSorted(all) || (total > 0 && all[total-1] != total-1) {
			t.Fatalf("unsharded selection of %d is not every source in order: %v", total, all)
		}
		for n := 1; n <= 5; n++ {
			seen := make([]int, total)
			for i := range n {
				s := shardSpec{i, n}.selection(total)
				if !slices.IsSorted(s) {
					t.Fatalf("shard %d/%d of %d out of inventory order: %v", i, n, total, s)
				}
				for _, k := range s {
					if k%n != i {
						t.Fatalf("shard %d/%d selected %d", i, n, k)
					}
					seen[k]++
				}
			}
			for k, c := range seen {
				if c != 1 {
					t.Fatalf("%d shards of %d: source %d selected %d times (union not exact or not disjoint)", n, total, k, c)
				}
			}
		}
	}
}

func TestShardArgumentsKeepTheMaxOnce(t *testing.T) {
	for _, bad := range []string{"", "1", "2/2", "-1/2", "0/0", "a/2", "0/2/3", "01/2", "0/+2"} {
		if _, err := parseShard(bad); err == nil {
			t.Errorf("accepted -shard %q", bad)
		}
	}
	cfg, err := parseCampaign([]string{"/r", "qenv", "22"})
	if err != nil || cfg.Shard != (shardSpec{0, 1}) || cfg.Max != 22 {
		t.Fatalf("default is not one unsharded campaign: %+v %v", cfg, err)
	}
	if cfg, err := parseCampaign([]string{"/r", "-shard", "0/1", "qenv", "22"}); err != nil || cfg.Max != 22 {
		t.Fatalf("-shard 0/1 is the unsharded campaign: %+v %v", cfg, err)
	}
	if _, err := parseCampaign([]string{"/r", "-shard", "1/4", "qenv", "22"}); err == nil || !strings.Contains(err.Error(), "applied once") {
		t.Fatalf("a shard accepted a max of its own: %v", err)
	}
	if cfg, err := parseCampaign([]string{"/r", "-shard", "3/4", "qenv"}); err != nil || cfg.Shard != (shardSpec{3, 4}) {
		t.Fatalf("%+v %v", cfg, err)
	}
	for _, g := range []struct {
		policy    gatePolicy
		survivors int
		want      string
	}{
		{unsharded(2), 2, gatePassed}, {unsharded(2), 3, gateFailed}, {unsharded(-1), 9, gatePassed},
		{gatePolicy{Max: -1, Shards: 4}, 0, gateDeferred}, {gatePolicy{Max: 0, Shards: 2}, 9, gateDeferred},
	} {
		if got := g.policy.verdict(g.survivors); got != g.want {
			t.Errorf("%+v with %d survivors: %s, want %s", g.policy, g.survivors, got, g.want)
		}
	}
}

func outcomeOf(file string, ordinal int, descriptor string) outcomeRecord {
	return outcomeRecord{Source: sourceRecord{File: file, Ordinal: ordinal, Descriptor: descriptor}, Outcome: "survived"}
}

// Repeated identical descriptors are distinct sources by file-local ordinal;
// the union check compares source identities, not descriptors or local IDs.
func TestUnionIsExactAndDisjoint(t *testing.T) {
	inv := candidateInventory{Count: 3, Sources: []sharedSource{{File: "p/a.go", Ordinal: 0, Descriptor: "a.go:1 f: drop call (f())"}, {File: "p/a.go", Ordinal: 1, Descriptor: "a.go:1 f: drop call (f())"}, {File: "p/b.go", Ordinal: 0, Descriptor: "b.go:1 g: negate if (x)"}}}
	a0, a1, b0 := outcomeOf("p/a.go", 0, inv.Sources[0].Descriptor), outcomeOf("p/a.go", 1, inv.Sources[1].Descriptor), outcomeOf("p/b.go", 0, inv.Sources[2].Descriptor)
	if p := checkUnion(inv, [][]outcomeRecord{{a0, b0}, {a1}}); len(p) != 0 {
		t.Fatalf("exact disjoint union refused: %v", p)
	}
	for _, tc := range []struct {
		name   string
		shards [][]outcomeRecord
		want   string
	}{
		{"overlap", [][]outcomeRecord{{a0, b0}, {a1, a0}}, "not disjoint"},
		{"overlap of a repeated descriptor", [][]outcomeRecord{{a0, a1, b0}, {a1}}, "not disjoint"},
		{"missing", [][]outcomeRecord{{a0}, {a1}}, "resolved by no shard"},
		{"repeated descriptor counted once", [][]outcomeRecord{{a0, b0}, {}}, "p/a.go ordinal 1"},
		{"extra", [][]outcomeRecord{{a0, b0}, {a1, outcomeOf("p/c.go", 0, "c")}}, "not in the inventory"},
		{"descriptor", [][]outcomeRecord{{a0, outcomeOf("p/b.go", 0, "other")}, {a1}}, "outcome descriptor"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := strings.Join(checkUnion(inv, tc.shards), "\n")
			if !strings.Contains(p, tc.want) {
				t.Fatalf("want %q, got %q", tc.want, p)
			}
		})
	}
}

// shardedFixture's one suite registers five mutants: two identical
// descriptors at file-local ordinals 0 and 1 of source.go, one more there and
// two in other.go. Local IDs 0 and 3 fail; the other three survive. In the
// inventory's order (other.go first), shard 0/2 takes indices 0, 2 and 4 (one
// survivor) and shard 1/2 indices 1 and 3 (two): each under a max of 2, their
// sum of 3 over it.
const shardedGenerator = `package main
import("os";"strings")
func main(){
 var out string
 for i,a:=range os.Args {if a=="-o"&&i+1<len(os.Args){out=os.Args[i+1]}}
 lines:=[]string{"qmut: mutant 0 sample/source.go:1 f: drop call (f())","qmut: mutant 1 sample/source.go:1 f: drop call (f())","qmut: mutant 2 sample/source.go:2 g: negate if (x)","qmut: mutant 3 sample/other.go:1 h: drop call (h())","qmut: mutant 4 sample/other.go:2 h: drop call (k())"}
 if os.Getenv("FIXTURE_EXTRA")!=""{lines=append(lines,"qmut: mutant 5 sample/other.go:3 z: drop call (z())")}
 script:="#!/bin/sh\ncase \"$GOTOGETHER_MUTANT\" in\nlist) printf '%s\\n' '"+strings.Join(lines,"' '")+"'; exit 0;;\n0|3) exit 1;;\n1|2|4) [ -n \"$FIXTURE_SLEEP\" ] && exec sleep 60;;\nesac\nexit 0\n"
 if err:=os.WriteFile(out,[]byte(script),0700);err!=nil{panic(err)}
}
`

func shardedFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range map[string]string{
		"go.mod":                    "module github.com/chucklehead-dev/gotogether\n\ngo 1.27.3\n",
		"sample/source.go":          "package sample\nfunc f() {}\n",
		"sample/other.go":           "package sample\nfunc h() {}\n",
		"sample/source_test.go":     "package sample\nimport \"testing\"\nfunc TestFixture(t *testing.T) {}\n",
		"qweave/go.mod":             "module fixture/qweave\n\ngo 1.27.3\n",
		"qweave/cmd/qweave/main.go": shardedGenerator,
	} {
		writeInputFixture(t, root, name, body)
	}
	return root
}

type campaignRun struct {
	dir    string // the run's artifact: its journal and sidecars
	output string
	exit   int
}

func runFixtureCampaign(t *testing.T, helper, root, name string, env map[string]string, args ...string) campaignRun {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	env["GOTOGETHER_MUTATION_LOG"] = filepath.Join(dir, "journal.jsonl")
	cmd := exec.CommandContext(t.Context(), helper, append([]string{root}, args...)...)
	cmd.Env = supervisorFixtureEnv(env)
	out, err := cmd.CombinedOutput()
	r := campaignRun{dir: dir, output: string(out)}
	if exit, ok := errors.AsType[*exec.ExitError](err); ok {
		r.exit = exit.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return r
}

// evidence lays runs out as the gate job downloads them: a directory per
// shard artifact.
func evidence(t *testing.T, runs ...campaignRun) string {
	t.Helper()
	dir := t.TempDir()
	for i, r := range runs {
		if err := os.CopyFS(filepath.Join(dir, "mutations-sample-shard-"+string(rune('a'+i))), os.DirFS(r.dir)); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestShardedCampaignAggregatesOnceAndFailsClosed(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the executable supervisor and runs several fixture campaigns")
	}
	helper := filepath.Join(t.TempDir(), "mutatecore")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", helper, ".")
	build.Env = supervisorFixtureEnv(nil)
	if b, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build supervisor: %v\n%s", err, b)
	}
	root := shardedFixture(t)
	env := func(extra ...string) map[string]string {
		m := map[string]string{"FIXTURE_EXTRA": "", "FIXTURE_SLEEP": ""}
		for i := 0; i+1 < len(extra); i += 2 {
			m[extra[i]] = extra[i+1]
		}
		return m
	}
	whole := runFixtureCampaign(t, helper, root, "whole", env(), "sample", "2")
	if whole.exit != 1 || !strings.Contains(whole.output, "3 survivors exceed limit 2") {
		t.Fatalf("unsharded campaign over its max not red: %d\n%s", whole.exit, whole.output)
	}
	s0 := runFixtureCampaign(t, helper, root, "s0", env(), "-shard", "0/2", "sample")
	s1 := runFixtureCampaign(t, helper, root, "s1", env(), "-shard", "1/2", "sample")
	for _, s := range []campaignRun{s0, s1} {
		if s.exit != 0 || !strings.Contains(s.output, "no limit applied here") {
			t.Fatalf("a complete shard applied a limit or failed: %d\n%s", s.exit, s.output)
		}
	}
	var r0, r1, rw completionReceipt
	for _, x := range []struct {
		run campaignRun
		r   *completionReceipt
	}{{s0, &r0}, {s1, &r1}, {whole, &rw}} {
		var err error
		if *x.r, _, err = readReceipt(filepath.Join(x.run.dir, "journal.jsonl.receipt.json")); err != nil {
			t.Fatal(err)
		}
	}
	if r0.Complete != (completeRecord{3, 1, gateDeferred}) || r1.Complete != (completeRecord{2, 2, gateDeferred}) || rw.Complete != (completeRecord{5, 3, gateFailed}) {
		t.Fatalf("selection or counts: %+v %+v %+v", r0.Complete, r1.Complete, rw.Complete)
	}
	if r0.InputDigest == r1.InputDigest {
		t.Fatal("shards' execution identities should differ (each run's journal path is in its environment)")
	}

	t.Run("unsharded campaign is the whole inventory", func(t *testing.T) {
		_, outcomes, err := validateJournalContent(filepath.Join(whole.dir, "journal.jsonl"), rw.Manifest)
		if err != nil || len(outcomes) != 5 || rw.Shared == nil || rw.Shared.Candidates.Count != 5 {
			t.Fatalf("%d outcomes, %v", len(outcomes), err)
		}
		for i, o := range outcomes {
			if o.Source.File != rw.Shared.Candidates.Sources[i].File || o.Source.Ordinal != rw.Shared.Candidates.Sources[i].Ordinal {
				t.Fatalf("outcome %d out of inventory order", i)
			}
		}
		if differingBlock(*rw.Shared, *r0.Shared) != "shard count" {
			t.Fatalf("an unsharded and a sharded campaign differ in more than their shard count: %s", differingBlock(*rw.Shared, *r0.Shared))
		}
	})

	t.Run("max applied once to the sum", func(t *testing.T) {
		dir := evidence(t, s0, s1)
		cmd := exec.CommandContext(t.Context(), helper, "aggregate", "sample", "2", "2", dir)
		out, err := cmd.CombinedOutput()
		if exit, ok := errors.AsType[*exec.ExitError](err); !ok || exit.ExitCode() != 1 || !strings.Contains(string(out), "3 survivors across 2 shards exceed limit 2") {
			t.Fatalf("summed survivors over the max not red: %v\n%s", err, out)
		}
		var receipt aggregateReceipt
		if err := readJSONFile(filepath.Join(dir, "sample.aggregate.json"), &receipt); err != nil {
			t.Fatal(err)
		}
		if receipt.Total != 5 || receipt.Survivors != 3 || receipt.GatePassed || receipt.AdmissionEligible || receipt.AggregateConcurrency != 2 || receipt.JobsPerShard != 1 || len(receipt.Executions) != 2 || len(receipt.Survived) != 3 {
			t.Fatalf("aggregate receipt: %+v", receipt)
		}
		if receipt.Total != rw.Complete.Total || receipt.Survivors != rw.Complete.Survivors {
			t.Fatal("aggregate differs from the unsharded campaign")
		}
		if receipt.Executions[0].InputDigest == receipt.Executions[1].InputDigest {
			t.Fatal("execution identities collapsed")
		}
		if got, err := aggregate("sample", 3, 2, evidence(t, s0, s1)); err != nil || !got.GatePassed {
			t.Fatalf("summed survivors at the max: %+v %v", got, err)
		}
	})

	extra := runFixtureCampaign(t, helper, root, "extra", env("FIXTURE_EXTRA", "1"), "-shard", "1/2", "sample")
	jobs := runFixtureCampaign(t, helper, root, "jobs", map[string]string{"JOBS": "2", "FIXTURE_EXTRA": "", "FIXTURE_SLEEP": ""}, "-shard", "1/2", "sample")
	of3 := runFixtureCampaign(t, helper, root, "of3", env(), "-shard", "1/3", "sample")
	for _, r := range []campaignRun{extra, jobs, of3} {
		if r.exit != 0 {
			t.Fatalf("fixture shard failed: %s", r.output)
		}
	}
	canceled := cancelFixtureShard(t, helper, root)

	for _, tc := range []struct {
		name   string
		runs   func() string
		shards int
		want   string
	}{
		{"missing shard", func() string { return evidence(t, s0) }, 2, "missing shard 1/2"},
		{"duplicate shard", func() string { return evidence(t, s0, s1, s1) }, 2, "duplicate shard 1/2"},
		{"inventory differs", func() string { return evidence(t, s0, extra) }, 2, "candidate inventory"},
		{"shared configuration differs", func() string { return evidence(t, s0, jobs) }, 2, "configuration"},
		{"shard of another split", func() string { return evidence(t, s0, s1, of3) }, 2, "not one of 2"},
		{"row's shard count differs", func() string { return evidence(t, s0, s1) }, 3, "missing shard 2/3"},
		{"canceled shard", func() string { return evidence(t, s0, s1, canceled) }, 2, "aborted (canceled or failed)"},
		{"unsealed journal", func() string {
			dir := evidence(t, s0, s1)
			mustRemove(t, filepath.Join(dir, "mutations-sample-shard-b", "journal.jsonl.receipt.json"))
			return dir
		}, 2, "complete but never sealed"},
		{"incomplete journal", func() string {
			dir := evidence(t, s0, s1)
			j := filepath.Join(dir, "mutations-sample-shard-b", "journal.jsonl")
			mustRemove(t, j+".receipt.json")
			b, _ := os.ReadFile(j)
			if err := os.WriteFile(j, b[:len(b)/2], 0600); err != nil {
				t.Fatal(err)
			}
			return dir
		}, 2, "incomplete: no completion receipt"},
		{"journal changed after sealing", func() string {
			dir := evidence(t, s0, s1)
			j := filepath.Join(dir, "mutations-sample-shard-b", "journal.jsonl")
			b, _ := os.ReadFile(j)
			if err := os.WriteFile(j, []byte(strings.Replace(string(b), `"outcome":"survived"`, `"outcome":"killed"`, 1)), 0600); err != nil {
				t.Fatal(err)
			}
			return dir
		}, 2, "journal differs from the one its receipt sealed"},
		{"receipt not in the current shape", func() string {
			dir := evidence(t, s0, s1)
			p := filepath.Join(dir, "mutations-sample-shard-b", "journal.jsonl.receipt.json")
			b, _ := os.ReadFile(p)
			if err := os.WriteFile(p, []byte(strings.Replace(string(b), `"version":2,`, `"version":2, `, 1)), 0600); err != nil {
				t.Fatal(err)
			}
			return dir
		}, 2, "not in the current strict shape"},
		{"no evidence", func() string { return t.TempDir() }, 2, "no sealed shard receipt"},
		{"another package", func() string { return evidence(t, s0, s1) }, 2, "not other"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pkg := "sample"
			if tc.name == "another package" {
				pkg = "other"
			}
			dir := tc.runs()
			_, err := aggregate(pkg, 9, tc.shards, dir)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want a failure naming %q, got %v", tc.want, err)
			}
			if _, statErr := os.Stat(filepath.Join(dir, pkg+".aggregate.json")); !os.IsNotExist(statErr) {
				t.Fatal("a failed aggregate published a receipt")
			}
		})
	}
}

func mustRemove(t *testing.T, path string) {
	t.Helper()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
}

// cancelFixtureShard terminates a shard while its surviving mutants run: its
// journal is aborted and it publishes no receipt.
func cancelFixtureShard(t *testing.T, helper, root string) campaignRun {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "canceled")
	log := filepath.Join(dir, "journal.jsonl")
	cmd := exec.CommandContext(t.Context(), helper, root, "-shard", "1/2", "sample")
	cmd.Env = supervisorFixtureEnv(map[string]string{"FIXTURE_SLEEP": "1", "FIXTURE_EXTRA": "", "GOTOGETHER_MUTATION_LOG": log})
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(60 * time.Second)
	for {
		b, _ := os.ReadFile(log)
		if strings.Contains(string(b), `"event":"admitted"`) {
			break
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			t.Fatal("shard never admitted its inventory")
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond) // the selected survivor is now sleeping
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("canceled shard exited 0")
	}
	if _, err := os.Stat(log + ".receipt.json"); !os.IsNotExist(err) {
		t.Fatal("canceled shard published a receipt")
	}
	return campaignRun{dir: dir}
}

// Concurrent go invocations are captured in any order; the generated bytes,
// not that order, are the generator's identity.
func TestGeneratedIdentityIgnoresCaptureOrderOnly(t *testing.T) {
	a := anchorsFor("/one/root", map[string]string{"GOROOT": "/go"})
	b := anchorsFor("/two/root", map[string]string{"GOROOT": "/go"})
	build := func(root, sha string) capturedInvocation {
		return capturedInvocation{Arguments: []string{"test", "-overlay=/tmp/x" + sha}, Inputs: []generatedInput{{Source: root + "/p/b.go", File: fileIdentity{Path: "/cap/1", SHA256: sha, Size: 2}}, {Source: root + "/p/a.go", File: fileIdentity{Path: "/cap/0", SHA256: "aa", Size: 1}}}}
	}
	list := capturedInvocation{Arguments: []string{"list", "-e"}}
	d1, _ := generatedDigest(a, []capturedInvocation{list, build("/one/root", "11"), build("/one/root", "22")})
	d2, _ := generatedDigest(b, []capturedInvocation{build("/two/root", "22"), list, list, build("/two/root", "11")})
	if d1 != d2 {
		t.Fatal("capture order or checkout root changed the generated identity")
	}
	d3, _ := generatedDigest(a, []capturedInvocation{build("/one/root", "11"), build("/one/root", "33")})
	if d3 == d1 {
		t.Fatal("different generated bytes have one identity")
	}
}
