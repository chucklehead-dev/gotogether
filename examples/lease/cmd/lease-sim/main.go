// Command lease-sim runs the lock, the store and the holders in simulated
// environments, one per seed, and writes each run's model steps to
// <out>/seed<N>.jsonl for `gotogether validate`.
//
//	lease-sim -seeds 20 -mutation no-fencing -clocks -out /tmp/runs
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/chucklehead-dev/gotogether/examples/lease"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
)

func main() {
	seeds := flag.Int("seeds", 20, "runs, with seeds 1..N")
	mut := flag.String("mutation", "", "no-fencing | wall-interval | absolute-expiry")
	clocks := flag.Bool("clocks", false, "clock faults: skews (1 s) and jumps (5 s)")
	drift := flag.Float64("drift", 0, "clocks run fast or slow by this fraction (0.25: 15 s a minute)")
	out := flag.String("out", "runs", "directory for the step logs")
	flag.Parse()
	m, ok := map[string]lease.Mutation{"": lease.Faithful, "no-fencing": lease.NoFencing, "wall-interval": lease.WallInterval, "absolute-expiry": lease.AbsoluteExpiry}[*mut]
	if !ok {
		fail(fmt.Errorf("unknown mutation %q (want no-fencing, wall-interval or absolute-expiry)", *mut))
	}
	cl := qenv.Clocks{Drift: *drift}
	if *clocks {
		cl.Skew, cl.Jump = time.Second, 5*time.Second
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		fail(err)
	}
	for seed := 1; seed <= *seeds; seed++ {
		sink, err := qobs.FileSink(filepath.Join(*out, fmt.Sprintf("seed%d.jsonl", seed)), qobs.PerStep)
		if err != nil {
			fail(err)
		}
		c := qenv.NewSeeded(uint64(seed))
		c.Rates = qenv.DefaultRates.Set("clock.skew", 0.5).Set("clock.drift", 1).Set("nemesis", 0.05) // as the tests': a jump in most runs
		rec := qobs.NewRecorder(sink, qobs.WithActor(fmt.Sprintf("seed%d", seed)))
		if _, err := lease.Run(qenv.WithContext(c, qobs.WithRecorder(context.Background(), rec)), m, cl); err != nil {
			fail(err)
		}
		if err := sink.Close(); err != nil {
			fail(err)
		}
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "lease-sim:", err)
	os.Exit(2)
}
