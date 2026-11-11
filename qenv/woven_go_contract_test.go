package qenv_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/woven"
)

// Exercise the ABI invoked by a rewritten //qenv:go statement. The child must
// run as a separate simulated task, retaining its parent's process context.
func TestWovenGoRunsChildInParentProcess(t *testing.T) {
	s := qenv.New(qenv.Nominal{})
	s.Check = true
	defer s.Close()
	var events []string
	var childProcess *qenv.Process
	var childSimulation *qenv.Sim
	p := s.Process("worker", false, func(context.Context) {
		events = append(events, "before")
		woven.Go(func() {
			childProcess = qenv.ProcessOf(context.Background())
			childSimulation = qenv.From(context.Background())
			events = append(events, "child")
		})()
		events = append(events, "after")
	})
	if err := s.Run(time.Second); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(events, []string{"before", "after", "child"}) || childProcess != p || childSimulation != s {
		t.Fatalf("events=%v child process=%v simulation=%p; want deferred child in process %s and simulation %p", events, childProcess, childSimulation, p.Name, s)
	}
}
