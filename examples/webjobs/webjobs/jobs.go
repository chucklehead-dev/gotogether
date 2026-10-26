package webjobs

import (
	"fmt"
	"math/rand"
	"sync"
	"time"
)

// A job's status, as the server holds it and the page shows it.
const (
	Accepted  = "accepted" // running: its report is being written
	Ready     = "ready"
	Cancelled = "cancelled"
)

type job struct {
	id, key string
	status  string
	stop    chan struct{} // closed by a cancel
}

// Jobs runs the reports: a job per submission key, each in the
// background for a while, then committed (ready) unless cancelled first.
// Every change of a job's status is made under mu and recorded there, so
// the server's steps are in the order its effects took place, and a job
// is visible as ready only once its commit is recorded.
type Jobs struct {
	mu    sync.Mutex
	jobs  map[string]*job
	byKey map[string]string
	n     int
	rng   *rand.Rand
	min   time.Duration
	max   time.Duration
	rec   recorder
	wg    sync.WaitGroup
}

func newJobs(rec recorder, seed int64, min, max time.Duration) *Jobs {
	return &Jobs{jobs: map[string]*job{}, byKey: map[string]string{}, rng: rand.New(rand.NewSource(seed)), min: min, max: max, rec: rec}
}

// Submit creates the job of key, unless key has one already: a page that
// sends its submission twice (a double click, a retry) gets the job it
// got the first time. created says whether this call made it.
func (s *Jobs) Submit(key string) (id, status string, created bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id, ok := s.byKey[key]; ok {
		return id, s.jobs[id].status, false
	}
	s.n++
	j := &job{id: fmt.Sprintf("j%d", s.n), key: key, status: Accepted, stop: make(chan struct{})}
	s.jobs[j.id], s.byKey[key] = j, j.id
	s.rec.record("created", "key", key, "id", j.id)
	work := s.min + time.Duration(s.rng.Int63n(int64(s.max-s.min)+1))
	s.wg.Add(1)
	go s.run(j, work)
	return j.id, j.status, true
}

// run writes j's report, which takes work, and commits it unless j was
// cancelled meanwhile.
func (s *Jobs) run(j *job, work time.Duration) {
	defer s.wg.Done()
	select {
	case <-time.After(work):
	case <-j.stop:
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if j.status != Accepted {
		return // cancelled while the timer fired
	}
	j.status = Ready
	s.rec.record("finished", "id", j.id)
}

// Status is job id's status.
func (s *Jobs) Status(id string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return "", false
	}
	return j.status, true
}

// Cancel stops job id if it is still running. It returns the job's
// status after the call and whether this call cancelled it.
func (s *Jobs) Cancel(id string) (status string, cancelled, found bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return "", false, false
	}
	if j.status != Accepted {
		return j.status, false, true // too late: it finished, or was cancelled
	}
	j.status = Cancelled
	close(j.stop)
	s.rec.record("cancelled", "id", id)
	return j.status, true, true
}

// Wait waits for every job to end.
func (s *Jobs) Wait() { s.wg.Wait() }
