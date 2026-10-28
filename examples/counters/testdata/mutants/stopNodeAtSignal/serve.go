package counters

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/chucklehead-dev/gotogether/grist"
)

// Serve is the app's binary's serve options (cmd/counters, and the pods
// of TestRealTwoPods and TestRealRollingRestart, which run the test
// binary as it): its mailer writes each welcome to stderr, in process,
// recording each send with the node's steps (mailerSendServed); its bank
// is the team's own (NewBank), served beside the node behind
// grist.ServeCalls on a loopback port and called over HTTP
// (grist.ServeDependency); its drain is Drain. The node's identity is the
// environment's (grist.NewIdentity): its pod's name and a fresh
// incarnation per start.
func Serve() []grist.ServeOption {
	mailer := stderrMailer()
	return []grist.ServeOption{grist.Dependencies(func(ctx context.Context) context.Context {
		return Mailer.With(NodeContext(ctx), mailer)
	}), grist.ServeDependency(Bank, NewBank()), grist.WithDrain(Drain)}
}

// Drain is the app's drain at SIGTERM (Serve's, and the simulated
// cluster's: gristsim.Cluster.Drain): it keeps serving, not ready, for
// grist.DefaultDrainDelay (its default on an orchestrator) wherever it runs, so the endpoints routing to it
// (a Kubernetes Service's, a Gateway's, the real rolling restart's
// router polling /readyz every 200 ms) stop before its listener closes;
// grist's other phases keep their defaults.
var Drain = grist.Drain{Delay: grist.DefaultDrainDelay}

// NodeContext is the context the node's work and requests run with.
//
// The mutant stopNodeAtSignal: the node's work cancelled at the signal
// (grist.Terminating), the bug grist's Main once had (its comment on the
// node's context records it): an at-most-once payout whose bank call is
// in flight at SIGTERM is cancelled, its outcome unknown (grist.unknown)
// though the bank may have paid.
func NodeContext(ctx context.Context) context.Context { return grist.Terminating(ctx) }

// stderrMailer sends a mail by writing it to stderr.
func stderrMailer() grist.Transport {
	var mu sync.Mutex
	return grist.Local(grist.Handlers{Send.Handler(func(ctx context.Context, m Mail) (Mailed, error) {
		mu.Lock()
		defer mu.Unlock()
		_, err := fmt.Fprintf(os.Stderr, "mail %s: created %s\n", grist.CallKey(ctx), m.Name)
		return Mailed{}, err
	})})
}
