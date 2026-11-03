package qenv_test

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/internal/skip"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qtrace"
	"github.com/chucklehead-dev/gotogether/validate"
)

// This bounded reference judges logical admissions and releases, independently
// of the Store's counter, using a set of fixed transaction identities. It does
// not claim physical max_connections fidelity. Reset/crash/cancellation have
// separate runtime controls in store_capacity_test.go.
func TestStoreCapacityAgainstQuint(t *testing.T) {
	skip.Quint(t)
	base, err := validate.NewChecker("testdata/store_capacity.yaml", validate.Options{Eval: "quint", Backend: "rust", Dir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	for _, limit := range []int{1, 2} {
		t.Run(strconv.Itoa(limit), func(t *testing.T) {
			steps, refused := capacityModelHistory(t, limit, false)
			checker, err := base.WithConstants(map[string]string{"LIMIT": strconv.Itoa(limit)})
			if err != nil {
				t.Fatal(err)
			}
			report, err := checker.Check(t.Context(), steps)
			if err != nil || !report.OK() {
				t.Fatalf("faithful capacity%d: error=%v report=%+v", limit, err, report)
			}
			if len(report.Actors) != 1 || report.Actors[0].Conforms != validate.Pass {
				t.Fatalf("faithful trace not judged: %s", report.Summary())
			}
			// Disable the feature under the same workload: the unlimited Store's
			// actual over-capacity acceptance must fail the bounded model. This
			// is an intentional contrast, not a defect in unlimited behavior.
			unlimited, probe := capacityModelHistory(t, limit, true)
			if probe != refused || !reflect.DeepEqual(unlimited[:probe], steps[:refused]) {
				t.Fatalf("contrast changed the prefix before the full-capacity attempt: %d vs %d", probe, refused)
			}
			rejected, err := checker.Check(t.Context(), unlimited)
			if err != nil {
				t.Fatalf("negative control was a tool/evidence error: %v", err)
			}
			if rejected.OK() || len(rejected.Actors) != 1 || rejected.Actors[0].Conforms != validate.Fail {
				t.Fatalf("accepted an admission beyond capacity: %s", rejected.Summary())
			}
			failure := rejected.Actors[0].Failure
			if failure == nil || failure.Index != refused || failure.Kind != "disabled" {
				t.Fatalf("wrong negative-control witness: %+v\n%s", failure, rejected.Summary())
			}
		})
	}
}

func capacityModelHistory(t *testing.T, limit int, unlimited bool) ([]qtrace.Step, int) {
	t.Helper()
	s := qenv.New(qenv.Nominal{})
	defer s.Close()
	st := qenv.NewStore(s, "model-capacity", qenv.Serializable)
	st.MaxTransactions = limit
	if unlimited {
		st.MaxTransactions = 0
	}
	handles := map[int]qenv.Txn{}
	live := map[int]bool{}
	var steps []qtrace.Step
	firstRefusal := -1
	record := func(action string, id int, admitted bool, active int) {
		steps = append(steps, qtrace.Step{Actor: "capacity", Recorder: "capacity-test", Thread: "client", Seq: uint64(len(steps) + 1), Action: action,
			Args: map[string]any{"id": int64(id), "admitted": admitted}, Obs: map[string]any{"active": int64(active)}})
	}
	s.Spawn("client", func(ctx context.Context) {
		begin := func(id int, want bool) {
			tx, err := st.Begin(ctx)
			admitted := err == nil
			if admitted != want || admitted && tx == nil {
				t.Fatalf("forced admission id%d: admitted=%v error=%v want=%v", id, admitted, err, want)
			}
			var active int
			if admitted {
				handles[id], live[id] = tx, true
				active = len(live)
			} else {
				var capacity *qenv.TransactionCapacityError
				if tx != nil || !errors.Is(err, qenv.ErrTransactionCapacity) || !errors.As(err, &capacity) || capacity.Limit != limit {
					t.Fatalf("refusal lacks actual capacity evidence: tx=%v error=%v", tx, err)
				}
				active = capacity.Active
				if firstRefusal < 0 {
					firstRefusal = len(steps)
				}
			}
			record("begin", id, admitted, active)
		}
		finish := func(id int) {
			handles[id].Rollback()
			delete(live, id)
			record("finish", id, false, len(live))
		}
		for id := 1; id <= limit; id++ {
			begin(id, true)
		}
		firstRefusal = len(steps)
		begin(limit+1, unlimited)
		if unlimited {
			// The counterexample ends at admission. Release all actual handles
			// without adding cleanup observations to that prefix.
			for id := 1; id <= limit+1; id++ {
				handles[id].Rollback()
			}
			return
		}
		finish(1)
		begin(limit+1, true)
		finish(1) // stale cleanup must not release the new transaction's lease
		begin(limit+2, false)
		for id := 2; id <= limit+1; id++ {
			finish(id)
		}
		begin(limit+2, true)
		finish(limit + 2)
	})
	if err := s.Run(time.Second); err != nil {
		t.Fatal(err)
	}
	if firstRefusal < 0 {
		t.Fatal("history never forced capacity exhaustion")
	}
	return steps, firstRefusal
}
