package qtest

import (
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// progressEvery is how often a long exploration says how far it got.
// Read-only.
var progressEvery = 10 * time.Second

// progressOut opens where progress lines go: the terminal itself, since
// go test holds back a package's output, stderr included, until it
// ends, when it tests several (a grist app's first `go test ./...` sat
// silent for six minutes). Nothing without a terminal (CI), nor under
// GOTOGETHER_PROGRESS=0. Process-wide: tests replace it.
var progressOut = func() (io.WriteCloser, error) {
	if os.Getenv("GOTOGETHER_PROGRESS") == "0" {
		return nil, os.ErrNotExist
	}
	return os.OpenFile("/dev/tty", os.O_WRONLY, 0)
}

// A progress counts an exploration's finished runs and, past
// progressEvery, writes a line every progressEvery to the terminal
// until it is stopped.
type progress struct {
	done    atomic.Int64
	judging atomic.Bool // the runs made, the model judging them
	stop    chan struct{}
	ended   sync.WaitGroup
}

// startProgress starts the progress of the exploration name: its runs
// so far (planned: how many it has planned), and the time since it
// began.
func startProgress(name string, planned func() int) *progress {
	p := &progress{stop: make(chan struct{})}
	p.ended.Add(1)
	go func() { // a ticker beside the runs, outside any simulation
		defer p.ended.Done()
		start := time.Now()
		tick := time.NewTicker(progressEvery)
		defer tick.Stop()
		var w io.WriteCloser
		for {
			select {
			case <-p.stop:
				if w != nil {
					w.Close()
				}
				return
			case <-tick.C:
				if w == nil {
					var err error
					if w, err = progressOut(); err != nil {
						return
					}
				}
				what := fmt.Sprintf("%d of %d runs made", p.done.Load(), planned())
				if p.judging.Load() {
					what = fmt.Sprintf("judging %d runs", planned())
				}
				fmt.Fprintf(w, "gotogether: %s: %s, %s\n", name, what, time.Since(start).Round(time.Second))
			}
		}
	}()
	return p
}

// end stops p.
func (p *progress) end() {
	close(p.stop)
	p.ended.Wait()
}
