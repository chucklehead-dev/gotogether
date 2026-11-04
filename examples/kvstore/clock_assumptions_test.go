package kvstore

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/examples/kvstore/kv"
	"github.com/chucklehead-dev/gotogether/qobs"
	"github.com/chucklehead-dev/gotogether/qtest"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// clockHistory keeps real Store observations and authors only their intervals.
// This is a reproducible clock-assumption fixture, not an NTP measurement or a
// deployment on two hosts. host-b's clock is 5ms ahead of host-a's.
func clockHistory(t *testing.T, stale bool) []qtrace.Step {
	t.Helper()
	mem := &qobs.MemorySink{}
	ctx := func(host string) context.Context {
		rec := qobs.NewRecorder(mem, qobs.WithID(host), qobs.WithActor("kv"), qobs.WithProcess("kv-service"))
		t.Cleanup(func() {
			if err := rec.Close(); err != nil {
				t.Error(err)
			}
		})
		return qobs.WithRecorder(context.Background(), rec)
	}
	writer, reader := ctx("host-a"), ctx("host-b")
	store := kv.NewStore()
	read := func(from *kv.Store) {
		res, err := from.Get(reader, "a")
		if !kv.IsNotFound(err) || res.Found || res.Revision != 0 {
			t.Fatalf("fixture read: %+v, %v; want an actual missing observation at revision zero", res, err)
		}
	}
	put := func() {
		if res := store.Put(writer, "a", "x"); res.Revision != 1 {
			t.Fatalf("fixture write: %+v; want revision one", res)
		}
	}
	if stale {
		put()
		// An empty lagging replica supplies a genuine stale observation,
		// rather than changing the faithful store's recorded return value.
		read(kv.NewStore())
	} else {
		read(store)
		put()
	}
	steps := mem.Steps()
	if len(steps) != 2 {
		t.Fatalf("recorded %d fixture steps; want one read and one write", len(steps))
	}
	at := func(ms int) time.Time { return time.Unix(100, 0).UTC().Add(time.Duration(ms) * time.Millisecond) }
	for i := range steps {
		s := &steps[i]
		switch s.Action {
		case "write":
			// host-a: true interval [2ms, 3ms].
			s.Invoked, s.Time = at(2), at(3)
		case "read":
			// Faithful: true [0ms, 1ms], recorded [5ms, 6ms].
			// Stale: true [5ms, 6ms], recorded [10ms, 11ms].
			start := 5
			if stale {
				start = 10
			}
			s.Invoked, s.Time = at(start), at(start+1)
		default:
			t.Fatalf("unexpected recorded fixture action %q", s.Action)
		}
	}
	slices.SortFunc(steps, func(a, b qtrace.Step) int { return a.Time.Compare(b.Time) })
	return steps
}

func TestTwoRecorderClockAssumptions(t *testing.T) {
	qtest.RequireQuint(t)
	steps := clockHistory(t, false)
	defaultRun := qtest.Validate(t, "kv.binding.yaml", steps, qtest.Linearize())
	defaultRun.Expect(t, qtest.Mismatch("read"))
	checkClockIssue(t, defaultRun.Report, "within 1ms", "closest 2ms apart")
	assumption := qtrace.ClockOrders(steps, qtrace.RealTimeSlack)
	if assumption.Orders != 1 || assumption.Skew != time.Millisecond || assumption.Closest != 2*time.Millisecond || !slices.Equal(assumption.Recorders, []string{"host-a", "host-b"}) {
		t.Fatalf("default clock orders: %+v", assumption)
	}
	declaredRun := qtest.Validate(t, "kv.binding.yaml", steps, qtest.ClockSkew(5*time.Millisecond))
	declaredRun.Expect(t, qtest.Conforms())
	if len(declaredRun.Steps) != 2 || declaredRun.Steps[0].Action != "read" || declaredRun.Steps[1].Action != "write" {
		t.Fatalf("want the actual read-before-write history, got %+v", declaredRun.Steps)
	}
	if assumption := qtrace.ClockOrders(steps, 5*time.Millisecond); assumption.Orders != 0 {
		t.Fatalf("the 5ms bound should leave these calls unordered: %+v", assumption)
	}
	// A stated clock bound does not excuse a stale read whose true interval
	// is after the write. Its recorded gap is 7ms, larger than either bound.
	stale := clockHistory(t, true)
	for _, skew := range []time.Duration{time.Millisecond, 5 * time.Millisecond} {
		t.Run("stale/"+skew.String(), func(t *testing.T) {
			run := qtest.Validate(t, "kv.binding.yaml", stale, qtest.ClockSkew(skew))
			run.Expect(t, qtest.Mismatch("read"))
			checkClockIssue(t, run.Report, "within "+skew.String(), "closest 7ms apart")
		})
	}
}

// The native CLI JSON document preserves the issue text describing the
// conditional clock assumption; there is no typed clock-evidence field here.
func checkClockIssue(t *testing.T, report *validate.Report, skew, gap string) {
	t.Helper()
	document := validate.NewDocument("validate")
	document.AddReport(report)
	data, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	var doc validate.Document
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	issues := slices.Clone(doc.Issues)
	for _, actor := range doc.Actors {
		issues = append(issues, actor.Issues...)
	}
	for _, issue := range issues {
		if strings.Contains(issue, "host-a, host-b") && strings.Contains(issue, skew) && strings.Contains(issue, gap) && strings.Contains(issue, "holds only if") {
			return
		}
	}
	t.Fatalf("native JSON lost the conditional clock assumption (%s, %s): %s", skew, gap, data)
}

// The shipped logs are regenerated from actual calls above. Changing the
// source's return values cannot silently leave a hand-authored fixture green.
func TestClockHistoryFixturesMatchStore(t *testing.T) {
	for _, tc := range []struct {
		name  string
		stale bool
	}{{"faithful", false}, {"stale", true}} {
		t.Run(tc.name, func(t *testing.T) {
			var got bytes.Buffer
			if err := qtrace.WriteJSONL(&got, clockHistory(t, tc.stale)); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join("testdata", "clocks", tc.name+".steps.jsonl")
			want, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("%v; fixture bytes:\n%s", err, got.Bytes())
			}
			if !bytes.Equal(got.Bytes(), want) {
				t.Fatalf("%s differs from the real Store observations with authored intervals:\n%s", file, got.Bytes())
			}
		})
	}
}
