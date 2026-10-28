package payments_test

// The migration, stage by stage (README.md): each test is one of the
// migration skill's checks, stated as the verdict it found.

import (
	"bufio"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/binding"
	"github.com/chucklehead-dev/gotogether/qtest"
	"github.com/chucklehead-dev/gotogether/validate"
)

// The legacy package is third-party code to the migration: it imports the
// standard library and the OpenTelemetry API (and what that imports),
// nothing of the toolkit.
func TestLegacyImportsNoToolkit(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "-f", "{{if not .Standard}}{{.ImportPath}}{{end}}", ".").CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	for _, p := range strings.Fields(string(out)) {
		if strings.HasPrefix(p, "github.com/chucklehead-dev/gotogether") && p != "github.com/chucklehead-dev/gotogether/examples/migrate-payments" {
			t.Errorf("the legacy package depends on %s: it imports the standard library and the OTel API alone", p)
		}
	}
}

// Stage 2: every requirement in the model has its provenance, two
// sources' kinds or `assumed`.
func TestRequirementsHaveProvenance(t *testing.T) {
	f, err := os.Open("model/payments.qnt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	reqs := map[string]string{} // R1 -> its comment block
	var cur string
	sc := bufio.NewScanner(f)
	start := regexp.MustCompile(`^\s*// req (R\d+):`)
	for sc.Scan() {
		line := sc.Text()
		if m := start.FindStringSubmatch(line); m != nil {
			cur = m[1]
			reqs[cur] = line
			continue
		}
		if cur != "" && strings.HasPrefix(strings.TrimSpace(line), "//   ") {
			reqs[cur] += "\n" + line
			continue
		}
		cur = ""
	}
	if len(reqs) < 9 {
		t.Fatalf("found %d requirements in model/payments.qnt, want R1..R9", len(reqs))
	}
	kind := regexp.MustCompile(`\b(code|test|log|trace|owner):`)
	for r, block := range reqs {
		kinds := map[string]bool{}
		for _, m := range kind.FindAllStringSubmatch(block, -1) {
			kinds[m[1]] = true
		}
		if len(kinds) < 2 && !strings.Contains(block, "assumed:") {
			t.Errorf("%s rests on %d kind of source and is not marked assumed:\n%s", r, len(kinds), block)
		}
	}
}

// Stage 3: the model's scenarios, each a requirement's lifecycle, are
// possible in it (the binding's simulate.possible says the same to
// gotogether simulate).
func TestScenariosPossible(t *testing.T) {
	qtest.Model(t, bindingFile).Expect(t, qtest.Possible(
		qtest.NamedScenario("R1 R3 R5 authorized, part captured, refunded in two, a third refused",
			qtest.Takes("authorize").With("id", `"ch_1"`).With("amount", "1000").Outcome("ok"),
			qtest.Takes("capture").With("id", `"ch_1"`).With("amount", "600").Outcome("ok"),
			qtest.Takes("refund").With("id", `"ch_1"`).With("amount", "400").Outcome("ok"),
			qtest.Takes("refund").With("id", `"ch_1"`).Outcome("ok"),
			qtest.Takes("refund").With("id", `"ch_1"`).Outcome("exceeds")),
		qtest.NamedScenario("R2 a retried key answers with its charge",
			qtest.Takes("authorize").With("key", `"k1"`).Outcome("ok"),
			qtest.Takes("authorize").With("key", `"k1"`).Outcome("replayed")),
		qtest.NamedScenario("R6 R8 a voided charge is not captured",
			qtest.Takes("void").With("id", `"ch_1"`).Outcome("ok"),
			qtest.Takes("capture").With("id", `"ch_1"`).Outcome("invalid_state")),
	))
}

// Stage 3, the draft: its capture takes the whole authorization (from
// capture's stale doc comment), so R3's partial capture is impossible in
// it: `gotogether simulate` prints FAIL for the binding's possible.
func TestDraftForbidsPartialCapture(t *testing.T) {
	qtest.RequireQuint(t)
	c, err := validate.NewChecker(draftBinding, validate.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	sim := c.Binding.Simulate
	var consts []string
	for k, v := range sim.Constants {
		consts = append(consts, k+"="+v)
	}
	rs, err := c.Possible(sim.Possible, validate.PossibleSearch{Constants: consts})
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 || rs[0].Found {
		t.Fatalf("the draft reaches R3's partial capture: %+v", rs)
	}
}

// Stage 4: the legacy tests' runs, recorded, conform to the model: the
// suite states nothing the model forbids. (They miss the legacy bug: no
// test refunds a charge twice.)
func TestLegacyTestsConform(t *testing.T) {
	qtest.RequireQuint(t)
	qtest.Runs(legacyRuns(t)...).Expect(t, qtest.All(qtest.Conforms()))
}

// Stage 4, the draft: TestPartialCapture's run is a capture the draft
// cannot take. Decided: the model's mistake (R3).
func TestDraftRejectsPartialCaptureTest(t *testing.T) {
	qtest.RequireQuint(t)
	run := recorded(t, TestPartialCapture)
	qtest.Validate(t, draftBinding, withoutReads(run.Steps)).Expect(t, qtest.Rejects("capture"))
}

// Stage 4, the defect found before any change: the production log's
// refunds of ch_3 (700, then 500, of 1000 captured) break R5. The legacy
// code ran it; the tests above pass on it.
func TestProductionLogViolatesR5(t *testing.T) {
	qtest.RequireQuint(t)
	qtest.Validate(t, bindingFile, logSteps(t, productionLog)).Expect(t, qtest.Violates("refundsWithinCapture").At("refund"))
}

// Stage 4, the draft: the production log's second record is a client's
// retry answered with its charge (replayed), which the draft cannot take.
// Decided: an undocumented requirement (R2), with the log and the code
// as its sources.
func TestDraftRejectsTheLogsReplay(t *testing.T) {
	qtest.RequireQuint(t)
	qtest.Validate(t, draftBinding, withoutReads(logSteps(t, productionLog))).Expect(t, qtest.Rejects("authorize"))
}

// Stage 4: an older log, rotated, starts at audit_seq 4113. Its first
// step's state is not the model's init, so it cannot be judged whole.
// Decided: judge the newer, whole logs; a snapshot record at each log's
// head would let the old ones be judged.
func TestRotatedLogLostItsPrefix(t *testing.T) {
	qtest.RequireQuint(t)
	qtest.Validate(t, bindingFile, logSteps(t, rotatedLog)).Expect(t, qtest.HasIssue("lost-prefix"))
}

// Stage 4: the staging spans, converted by payments.convert.yaml, conform;
// every span is matched (the notifications by a drop). The draft's rule
// file left two operations unmatched: lookups (then modelled, R7) and
// the merchant notifications (outside the context).
func TestStagingSpans(t *testing.T) {
	qtest.RequireQuint(t)
	tds := readSpans(t, stagingSpans)
	steps, un := converted(t, rulesFile, tds)
	if len(un) != 0 {
		t.Errorf("payments.convert.yaml leaves spans unmatched: %v", un)
	}
	qtest.Validate(t, bindingFile, steps).Expect(t, qtest.Conforms())

	_, un = converted(t, draftRules, tds)
	var names []string
	for _, u := range un {
		names = append(names, u.Name)
	}
	if want := []string{"GET /charges/{id}", "notify merchant"}; !slices.Equal(names, want) {
		t.Errorf("the draft's rules leave %v unmatched, want %v", un, want)
	}
}

// Stage 4: the code records lookups (auditsteps' qobs.Record calls,
// which lint -src reads), and the draft has no such action:
// `gotogether lint -binding testdata/draft/payments.draft.yaml -src
// ./auditsteps` says lookup is not an action of payments. The model has
// every action the code records.
func TestDraftLacksRecordedLookup(t *testing.T) {
	qtest.RequireQuint(t)
	recs, err := binding.ScanRecords("auditsteps")
	if err != nil {
		t.Fatal(err)
	}
	missing := func(bind string) []string {
		c, err := validate.NewChecker(bind, validate.Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		var out []string
		for _, r := range recs {
			if _, ok := c.Model.Actions[r.Action]; !ok {
				out = append(out, r.Action)
			}
		}
		return out
	}
	if got := missing(draftBinding); !slices.Equal(got, []string{"lookup"}) {
		t.Errorf("recorded actions the draft lacks: %v, want [lookup]", got)
	}
	if got := missing(bindingFile); len(got) != 0 {
		t.Errorf("recorded actions the model lacks: %v", got)
	}
}

// Stage 6, after the fix: the production log's requests, replayed on the
// code as it is, conform. The second refund of ch_3 is now refused.
func TestProductionDayReplayedConforms(t *testing.T) {
	qtest.RequireQuint(t)
	recorded(t, replay(logSteps(t, productionLog))).Expect(t, qtest.Conforms())
}
