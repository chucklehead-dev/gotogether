package webjobs

import (
	"testing"
	"time"
)

// A job's id and its run are its key's (and the seed's): a run without
// k2's submission names and times k3's job as a run with it does, which
// is what lets a shrinker remove actions and rerun the rest. A key's
// second job (a server that made one) has an id of its own.
func TestJobsKeyed(t *testing.T) {
	jobs := func(keys ...string) map[string][2]any {
		s := newJobs(recorder{}, 7, 100*time.Millisecond, 300*time.Millisecond)
		out := map[string][2]any{}
		for _, k := range keys {
			id, d := s.next(k)
			out[k] = [2]any{id, d}
		}
		return out
	}
	all, some := jobs("k1", "k2", "k3"), jobs("k1", "k3")
	if all["k3"] != some["k3"] || all["k1"] != some["k1"] {
		t.Fatalf("k3's job with k2 %v, without %v: a job depends on another's", all["k3"], some["k3"])
	}
	if all["k1"] == all["k3"] {
		t.Fatalf("k1 and k3 got one job: %v", all["k1"])
	}
	if other := newJobs(recorder{}, 8, 100*time.Millisecond, 300*time.Millisecond); func() bool {
		_, d := other.next("k3")
		return d == all["k3"][1]
	}() && func() bool {
		_, d := other.next("k1")
		return d == all["k1"][1]
	}() {
		t.Errorf("seeds 7 and 8 drew the same runs for k1 and k3: the seed draws nothing")
	}
	s := newJobs(recorder{}, 7, 100*time.Millisecond, 300*time.Millisecond)
	a, _ := s.next("k1")
	b, _ := s.next("k1")
	if a == b || b != a+"-2" {
		t.Errorf("k1's two jobs %s, %s: want distinct ids, the second %s-2", a, b, a)
	}
}
