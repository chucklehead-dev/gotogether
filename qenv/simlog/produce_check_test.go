package simlog_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qenv/simlog"
)

// A lost acknowledgement legitimately retries one record. The existing
// broken-deduplication hook must be diagnosed by append itself: this scenario
// has no consumer or commit that could discover the duplicate later.
func TestProduceChecksBeforeAConsumerRuns(t *testing.T) {
	for _, broken := range []bool{false, true} {
		for _, check := range []bool{false, true} {
			t.Run(fmt.Sprintf("broken=%t/check=%t", broken, check), func(t *testing.T) {
				faults := &qenv.Script{Faults: []qenv.Scripted{{Kind: "log.events.produce", Name: "lostAck"}}}
				s := qenv.New(faults)
				defer s.Close()
				s.Check = check
				l := simlog.New(s, "events", 1)
				if broken {
					simlog.Break(l, "dedupe")
				}
				acknowledged, finished := 0, false
				s.Spawn("produce", func(ctx context.Context) {
					p := l.Producer(true)
					p.Produce(ctx, "key", []byte("one"), func(err error) {
						if err != nil {
							t.Errorf("produce callback: %v", err)
							return
						}
						acknowledged++
					})
					p.Flush(ctx)
					finished = true
				})
				var runErr error
				caught := func() (caught any) {
					defer func() { caught = recover() }()
					runErr = s.Run(time.Second)
					return nil
				}()
				if runErr != nil {
					t.Fatalf("simulation failed instead of completing or diagnosing the duplicate: %v", runErr)
				}
				if remaining, misnamed := faults.Remaining(), faults.Misnamed(); len(remaining) != 0 || len(misnamed) != 0 {
					t.Fatalf("lost acknowledgement was not exercised: remaining=%v misnamed=%v", remaining, misnamed)
				}
				if broken && check {
					message, ok := caught.(string)
					if !ok || !strings.HasPrefix(message, "simlog: log events, after a produce: ") || !strings.Contains(message, "a record landed twice, or out of order") || !strings.HasSuffix(message, " (GOTOGETHER_CHECK)") {
						t.Fatalf("panic = %#v, want the immediate produce duplicate diagnostic", caught)
					}
					if finished || acknowledged != 0 || len(l.Records(0)) != 2 {
						t.Fatalf("diagnosis timing: finished=%t acknowledgments=%d records=%d", finished, acknowledged, len(l.Records(0)))
					}
					return
				}
				want := "one"
				if broken {
					want = "one one"
				}
				if caught != nil || !finished || acknowledged != 1 || values(l, 0) != want {
					t.Fatalf("control: panic=%#v finished=%t acknowledgments=%d records=%q; want successful completion with %q", caught, finished, acknowledged, values(l, 0), want)
				}
			})
		}
	}
}
