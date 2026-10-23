package qtest

import (
	"context"
	"testing"

	"github.com/chucklehead-dev/gotogether/connect"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
)

var netLose = qenv.DeclareFaults("net.lose", "drop")

// sender sends three messages, each retried while the network drops it.
func sender(c qenv.Chooser) error {
	s := qenv.New(c)
	defer s.Close()
	s.Spawn("sender", func(ctx context.Context) {
		for sent := 0; sent < 3; {
			if s.FaultOf(netLose) == "drop" {
				continue // lost: send it again
			}
			sent++
			qobs.Record(ctx, "send", nil)
		}
	})
	return s.Run(0)
}

// The model's traces drive the sender through its environment: each
// trace's lose steps become the network's drops, at the points the trace
// has them, and the sender's recorded sends are checked against the
// model. FollowTraces fails a run that does not take its trace's faults.
func TestFollowTraces(t *testing.T) {
	RequireQuint(t)
	sys := System{Binding: "testdata/flaky.yaml", Run: sender}
	x := sys.FollowTraces(t, connect.Config{Spec: "testdata/flaky.qnt", MaxSamples: 8, MaxSteps: 6, Seed: "7"},
		connect.FaultMap{"lose": func(*connect.Step) *connect.Fault { return &connect.Fault{Kind: "net.lose", Name: "drop"} }})
	x.Expect(t, Conforms())
	drops := 0
	for _, r := range x.Runs {
		drops += len(r.Faults())
	}
	if len(x.Runs) != 8 || drops == 0 {
		t.Errorf("%d runs, %d drops: want the traces' losses as faults", len(x.Runs), drops)
	}
}
