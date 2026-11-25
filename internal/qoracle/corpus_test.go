package qoracle

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The corpus's tests. Per push: the manifest is current for the
// repository's files (and for quint's where QUINT_SRC names a fetched
// checkout), with no quint. The nightly (qfront-differential,
// GOTOGETHER_QFRONT_CORPUS=1): every entry through the harness, quint
// against its stub. GOTOGETHER_UPDATE_CORPUS=1 rewrites the manifest
// (quint and QUINT_SRC needed).

func roots(t *testing.T) Roots {
	t.Helper()
	return Roots{Repo: repoRoot(t), Quint: os.Getenv("QUINT_SRC")}
}

func pinnedCommit(t *testing.T, root string) (string, string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, "ci/quint-src.commit"))
	if err != nil {
		t.Fatal(err)
	}
	f := strings.Fields(string(b))
	if len(f) != 2 {
		t.Fatalf("ci/quint-src.commit: want one line, a version and its commit: %q", b)
	}
	return f[0], f[1]
}

func TestUpdateManifest(t *testing.T) {
	mode := os.Getenv("GOTOGETHER_UPDATE_CORPUS")
	if mode != "1" && mode != "entries" {
		t.Skip("GOTOGETHER_UPDATE_CORPUS=1 rewrites testdata/corpus.json (quint and QUINT_SRC needed); =entries re-surveys those whose id contains GOTOGETHER_QFRONT_ENTRY, and adds new ones")
	}
	requireQuint(t)
	r := roots(t)
	if r.Quint == "" {
		t.Fatal("QUINT_SRC must name quint's checkout at the tag (ci/fetch-quint-src.sh)")
	}
	ctx := t.Context()
	files, texts, err := Files(r)
	if err != nil {
		t.Fatal(err)
	}
	only := os.Getenv("GOTOGETHER_QFRONT_ENTRY")
	var gen []Entry
	gtexts := map[string]string{}
	var skipped []string
	if mode == "1" || strings.HasPrefix(only, SourceEval) || strings.HasPrefix(only, SourceInvariant) {
		if gen, gtexts, skipped, err = Generated(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	for _, s := range skipped {
		t.Logf("generated: skipped %s", s)
	}
	for k, v := range gtexts {
		texts[k] = v
	}
	all := append(files, gen...)
	var old *Manifest
	if mode == "entries" {
		if old, err = LoadManifest(); err != nil {
			t.Fatal(err)
		}
		known := map[string]bool{}
		for _, e := range old.Entries {
			known[e.ID] = true
		}
		var some []Entry
		for _, e := range all {
			if (only != "" && strings.Contains(e.ID, only)) || !known[e.ID] {
				some = append(some, e)
			}
		}
		all = some
	}
	start := time.Now()
	surveyed, errs := Survey(ctx, r, all, texts, t.TempDir(), 4)
	if old != nil {
		surveyed = merge(old.Entries, surveyed)
	}
	t.Logf("surveyed %d entries in %v", len(surveyed), time.Since(start))
	version, commit := pinnedCommit(t, r.Repo)
	m := &Manifest{Quint: version, Commit: commit, Entries: surveyed}
	if err := os.WriteFile("testdata/corpus.json", m.Encode(), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, err := range errs {
		t.Errorf("not surveyed: %v", err)
	}
}

// The manifest pins every .qnt file of the repository (and, with
// QUINT_SRC, quint's): each one listed, its text and constructs the
// pinned ones. Its entries keep quint's verdicts, which only quint can
// recompute: a change here is a GOTOGETHER_UPDATE_CORPUS=1 run.
func TestManifestCurrent(t *testing.T) {
	m, err := LoadManifest()
	if err != nil {
		t.Fatal(err)
	}
	r := roots(t)
	version, commit := pinnedCommit(t, r.Repo)
	if m.Quint != version || m.Commit != commit {
		t.Fatalf("the manifest is quint %s at %s, ci/quint-src.commit pins %s at %s: rewrite it (GOTOGETHER_UPDATE_CORPUS=1)", m.Quint, m.Commit, version, commit)
	}
	got, _, err := Files(r)
	if err != nil {
		t.Fatal(err)
	}
	var want []Entry
	for _, e := range m.Entries {
		switch e.Source {
		case SourceRepo:
			want = append(want, e)
		case SourceQuint, SourceQuintTest:
			if r.Quint != "" {
				want = append(want, e)
			}
		}
	}
	if issues := Check(want, got); len(issues) > 0 {
		t.Fatalf("the corpus manifest is stale (GOTOGETHER_UPDATE_CORPUS=1 QUINT_SRC=<ci/fetch-quint-src.sh's> go test -run TestUpdateManifest ./internal/qoracle/ rewrites it):\n  %s", strings.Join(issues, "\n  "))
	}
	for _, e := range m.Entries {
		if e.Expect != expectOf(e) {
			t.Errorf("%s: expect %q, its verdict and constructs say %q", e.ID, e.Expect, expectOf(e))
		}
	}
}

// What the corpus holds: §4.1's sources, each with entries, and the
// constructs decision 4 names.
func TestManifestCoverage(t *testing.T) {
	m, err := LoadManifest()
	if err != nil {
		t.Fatal(err)
	}
	bySource := map[string]int{}
	constructs := map[string]int{}
	codes := map[string]int{}
	for _, e := range m.Entries {
		bySource[e.Source]++
		for _, c := range e.Constructs {
			constructs[c]++
		}
		for _, c := range append(slices.Clone(e.Quint.Typecheck), e.Quint.Compile...) {
			codes[c]++
		}
	}
	for _, s := range []string{SourceRepo, SourceQuint, SourceQuintTest, SourceEval, SourceInvariant} {
		if bySource[s] == 0 {
			t.Errorf("no %s entries", s)
		}
	}
	for _, c := range []string{"instance", "export", "poly-alias", "sum-type", "match", "spread", "temporal", "assume", "destructuring", "apalache"} {
		if constructs[c] == 0 {
			t.Errorf("no entry uses %s", c)
		}
	}
	t.Logf("entries by source: %v", bySource)
	t.Logf("quint's codes over the corpus: %v", codes)
}

// keepDir is where a disagreement's shrunk program is kept (GOTOGETHER_KEEP).
func keepDir(t *testing.T) string {
	if k := os.Getenv("GOTOGETHER_KEEP"); k != "" {
		if err := os.MkdirAll(k, 0o755); err != nil {
			t.Fatal(err)
		}
		return k
	}
	return t.TempDir()
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// stub is the "front end" the nightly compares quint with, and the
// oracle's side: GOTOGETHER_QFRONT_STUB=quint is quint itself, shifted
// (the default) quint with every id moved, go Q2's Go front end at P1
// (its parse, against quint's, both cut to the P1 tier).
func stub(t *testing.T) (oracle, fe FrontEnd) {
	switch s := os.Getenv("GOTOGETHER_QFRONT_STUB"); s {
	case "", "shifted":
		return Quint{}, Shifted{}
	case "quint":
		return Quint{}, Quint{}
	case "go":
		return ParseTier{Quint{ParseOnly: true}}, GoParse{}
	default:
		t.Fatalf("GOTOGETHER_QFRONT_STUB=%s: want quint, shifted or go", s)
	}
	return nil, nil
}

// TestCorpus is the nightly's: every entry of the manifest (those whose
// id contains GOTOGETHER_QFRONT_ENTRY, if set) through the harness,
// quint against the stub; quint's verdict must still be the pinned one.
// A disagreement is shrunk by its declarations and kept (GOTOGETHER_KEEP)
// as a .qnt with both sides' canonical diff.
func TestCorpus(t *testing.T) {
	if os.Getenv("GOTOGETHER_QFRONT_CORPUS") != "1" {
		t.Skip("the nightly's (GOTOGETHER_QFRONT_CORPUS=1, quint and QUINT_SRC)")
	}
	requireQuint(t)
	r := roots(t)
	if r.Quint == "" {
		t.Fatal("QUINT_SRC must name quint's checkout at the tag (ci/fetch-quint-src.sh)")
	}
	m, err := LoadManifest()
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	_, texts, err := Files(r)
	if err != nil {
		t.Fatal(err)
	}
	only := os.Getenv("GOTOGETHER_QFRONT_ENTRY")
	needGenerated := false
	for _, e := range m.Entries {
		needGenerated = needGenerated || (e.Source == SourceEval && strings.Contains(e.ID, only))
	}
	if needGenerated {
		_, gtexts, skipped, err := Generated(ctx, r)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range skipped {
			t.Logf("generated: skipped %s", s)
		}
		for k, v := range gtexts {
			texts[k] = v
		}
	}
	oracle, fe := stub(t)
	_, parseOnly := oracle.(ParseTier)
	keep := keepDir(t)
	known := shiftKnown(t)
	var (
		mu                             sync.Mutex
		ran, agreed, stale, verdictOff int
		wg                             sync.WaitGroup
		sem                            = make(chan struct{}, 4)
	)
	for _, e := range m.Entries {
		if only != "" && !strings.Contains(e.ID, only) {
			continue
		}
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			tmp, err := os.MkdirTemp("", "qfront-")
			if err != nil {
				t.Error(err)
				return
			}
			defer os.RemoveAll(tmp)
			text := texts[e.ID]
			if e.Source == SourceEval && text != "" && sha(text) != e.SHA256 {
				// generated by the code as it is now: compared all the same
				mu.Lock()
				stale++
				mu.Unlock()
				t.Logf("%s: the generated module changed since the manifest (GOTOGETHER_UPDATE_CORPUS=1 rewrites it)", e.ID)
			}
			if e.Source == SourceEval && text == "" {
				t.Errorf("%s: its binding no longer generates a module", e.ID)
				return
			}
			p, err := e.Program(r, text, tmp)
			if err != nil {
				t.Error(err)
				return
			}
			oa, ob, err := RunBoth(ctx, p, oracle, fe, tmp)
			if err != nil {
				t.Errorf("%s: %v", e.ID, err)
				return
			}
			// a parse alone says no verdict: P1's comparison is its tier's
			v, err := VerdictOf(oa)
			if err == nil && !parseOnly && !sameVerdict(v, e.Quint) {
				mu.Lock()
				verdictOff++
				mu.Unlock()
				t.Errorf("%s: quint's verdict is %v, pinned %v: quint is not %s, or the entry changed", e.ID, v, e.Quint, m.Quint)
			}
			d, err := CompareOutputs(oa, ob)
			mu.Lock()
			ran++
			if d == nil && err == nil {
				agreed++
			}
			mu.Unlock()
			if err != nil {
				t.Errorf("%s: %v", e.ID, err)
				return
			}
			if reason, ok := known[e.ID]; ok && fe.Name() == (Shifted{}).Name() {
				if d == nil {
					t.Errorf("%s: no longer disagrees with quint shifted: take it out of testdata/shift-known.txt", e.ID)
				} else {
					t.Logf("%s: quint shifted differs, as known (%s): %s", e.ID, reason, d.Tier)
				}
				return
			}
			if d == nil {
				return
			}
			src := p.Text
			if src == "" {
				b, _ := os.ReadFile(p.File)
				src = string(b)
			}
			shrunk, tried := ShrinkDecls(ctx, p, src, d, oracle, fe, tmp)
			name := filepath.Join(keep, unsafeName.ReplaceAllString(e.ID, "_"))
			os.WriteFile(name+".qnt", []byte(shrunk), 0o644)
			os.WriteFile(name+".diff", []byte(d.String()+"\n"), 0o644)
			t.Errorf("%s: quint and %s disagree: %v\n  shrunk (%d candidates) to %s.qnt (GOTOGETHER_QFRONT_ENTRY=%s reruns it)", e.ID, fe.Name(), d, tried, name, strconv.Quote(e.ID))
		})
	}
	wg.Wait()
	t.Logf("%d entries compared, %d agreed (quint against %s); %d generated modules changed since the manifest; %d verdicts off the pinned", ran, agreed, fe.Name(), stale, verdictOff)
	if ran == 0 {
		t.Fatalf("no entry ran (GOTOGETHER_QFRONT_ENTRY=%q)", only)
	}
}

// shiftKnown are the entries on which quint's own verdict depends on its
// ids (testdata/shift-known.txt: an id, then # and the reason): quint
// shifted differs from quint there, and a Go front end, numbering its
// nodes otherwise, will meet the same. Each is a finding about quint, not
// the form's; an entry there that agrees fails, so the list stays true.
func shiftKnown(t *testing.T) map[string]string {
	b, err := os.ReadFile("testdata/shift-known.txt")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, l := range strings.Split(string(b), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		id, reason, ok := strings.Cut(l, " # ")
		if !ok {
			t.Fatalf("testdata/shift-known.txt: %q: want an id, \" # \" and the reason", l)
		}
		out[strings.TrimSpace(id)] = reason
	}
	return out
}

// merge is old with fresh's entries in place of theirs, and fresh's new
// ones after.
func merge(old, fresh []Entry) []Entry {
	byID := map[string]Entry{}
	for _, e := range fresh {
		byID[e.ID] = e
	}
	var out []Entry
	for _, e := range old {
		if f, ok := byID[e.ID]; ok {
			e = f
			delete(byID, e.ID)
		}
		out = append(out, e)
	}
	for _, e := range fresh {
		if _, ok := byID[e.ID]; ok {
			out = append(out, e)
		}
	}
	return out
}

func sameVerdict(a, b Verdict) bool {
	return slices.Equal(a.Typecheck, b.Typecheck) && slices.Equal(a.Compile, b.Compile)
}
