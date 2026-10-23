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

	"github.com/chucklehead-dev/gotogether/examples/lease"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
)

func main() {
	seeds := flag.Int("seeds", 20, "runs, with seeds 1..N")
	mut := flag.String("mutation", "", "no-fencing | absolute-expiry")
	clocks := flag.Bool("clocks", false, "clock faults: skews and jumps")
	out := flag.String("out", "runs", "directory for the step logs")
	flag.Parse()
	m, ok := map[string]lease.Mutation{"": lease.Faithful, "no-fencing": lease.NoFencing, "absolute-expiry": lease.AbsoluteExpiry}[*mut]
	if !ok {
		fail(fmt.Errorf("unknown mutation %q (want no-fencing or absolute-expiry)", *mut))
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		fail(err)
	}
	for seed := 1; seed <= *seeds; seed++ {
		sink, err := qobs.FileSink(filepath.Join(*out, fmt.Sprintf("seed%d.jsonl", seed)))
		if err != nil {
			fail(err)
		}
		c := qenv.NewSeeded(uint64(seed))
		c.Rates = qenv.DefaultRates.Set("clock.skew", 0.5)
		rec := qobs.NewRecorder(sink, qobs.WithActor(fmt.Sprintf("seed%d", seed)))
		if _, err := lease.Run(qenv.WithContext(c, qobs.WithRecorder(context.Background(), rec)), m, *clocks); err != nil {
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
