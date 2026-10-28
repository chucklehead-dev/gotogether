package qenv_test

import (
	"context"
	"fmt"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// A rolling restart of two web servers behind a load balancer whose view
// of them is 2 s late, a client calling through it all the while. Each
// server drains at SIGTERM as a Kubernetes pod should: it goes on
// serving for a delay, then closes its listener and exits, and is
// restarted. With no delay, the balancer still sends it requests for its
// lag after the listener closed, and they are refused (the client
// retries them); with a delay longer than the lag none is. The run is
// the same for every delay but the drain's: the same seed, the same
// choices.
func ExampleRouter() {
	for _, delay := range []time.Duration{0, 5 * time.Second} {
		c := qenv.NewSeeded(1)
		c.Rates = qenv.NoFaults.Set("route.web.lag", 1) // every change of the view late
		s := qenv.New(c)

		// each server incarnation's listener: closed once its drain shuts it
		closed := map[qenv.Endpoint]bool{}
		var servers []*qenv.Process
		for _, name := range []string{"web-0", "web-1"} {
			servers = append(servers, s.Process(name, false, func(ctx context.Context) {
				p := qenv.ProcessOf(ctx)
				term := qenv.Terminating(ctx) // cancelled at SIGTERM
				qenv.WaitIdle(ctx, func() bool { return term.Err() != nil })
				qenv.Sleep(ctx, delay) // not ready, still serving
				closed[qenv.Endpoint{Process: p, Incarnation: p.Incarnation}] = true
				p.Exit() // restarted as its next incarnation
			}))
		}
		s.Terminations(qenv.Termination{Grace: 30 * time.Second}, servers...)
		lb := qenv.NewRouter(s, "web", 2*time.Second, servers...)

		served, refused := 0, 0
		s.Spawn("client", func(ctx context.Context) {
			for range 200 {
				e, err := lb.Pick(ctx)
				if err != nil || e.Gone() || closed[e] {
					refused++ // no server took it: retried by a real client
				} else {
					served++
				}
				qenv.Sleep(ctx, 100*time.Millisecond)
			}
		})
		s.Spawn("rollout", func(ctx context.Context) {
			qenv.Sleep(ctx, time.Second)
			for _, p := range servers { // one at a time, the next once the last is back
				inc := p.Incarnation
				p.Terminate()
				qenv.Wait(ctx, func() bool { return p.Incarnation > inc && !p.Down() })
			}
		})
		if err := s.Run(time.Minute); err != nil {
			fmt.Println(err)
		}
		s.Close()
		fmt.Printf("drain delay %v: %d requests served, %d refused\n", delay, served, refused)
	}
	// Output:
	// drain delay 0s: 181 requests served, 19 refused
	// drain delay 5s: 200 requests served, 0 refused
}
