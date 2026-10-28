package counters

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/chucklehead-dev/gotogether/grist"
)

// Serve is the app's binary's serve options (cmd/counters, and the pods
// of TestRealTwoPods, which run the test binary as it): its mailer
// writes each welcome to stderr, in process, recording each send with
// the node's steps (mailerSendServed); its bank is the team's own
// (NewBank), served beside the node behind grist.ServeCalls on a
// loopback port and called over HTTP (grist.ServeDependency). The node's
// identity is the environment's (grist.IdentityFromEnv): its pod's name
// and a fresh incarnation per start.
func Serve() []grist.ServeOption {
	mailer := stderrMailer()
	return []grist.ServeOption{grist.Dependencies(func(ctx context.Context) context.Context {
		return Mailer.With(ctx, mailer)
	}), grist.ServeDependency(Bank, NewBank())}
}

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
