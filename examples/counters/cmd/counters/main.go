// Command counters is examples/counters' application as a binary
// (grist.Main): `counters catalog` prints its registry as JSON, `counters
// serve -db sqlite:counters.db -tenant t1 -steps steps.jsonl` serves its
// HTTP API and runs its node on a SQLite file, recording every step for
// validation with the example's bindings (run-demo.sh's SERVE=1 path).
//
// Its mailer writes each welcome to stderr. Its actor comes from the
// X-Actor header (-actor-header X-Actor): a demo's stand-in for the
// authentication a real deployment puts in front (grist.Middleware).
package main

import (
	"context"
	"fmt"
	"os"
	"sync"

	_ "modernc.org/sqlite" // serve's driver: -db sqlite:PATH

	"github.com/chucklehead-dev/gotogether/examples/counters"
	"github.com/chucklehead-dev/gotogether/grist"
)

// stderrMailer sends a mail by writing it to stderr.
type stderrMailer struct{ mu sync.Mutex }

func (m *stderrMailer) Send(_ context.Context, key, msg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, err := fmt.Fprintf(os.Stderr, "mail %s: %s\n", key, msg)
	return err
}

func main() {
	mailer := &stderrMailer{}
	grist.Main(counters.App, grist.Dependencies(func(ctx context.Context) context.Context {
		return counters.MailerDep.With(ctx, mailer)
	}))
}
