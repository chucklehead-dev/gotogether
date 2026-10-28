// Command requeue-sim runs the controller and the cloud in simulated
// environments, one per seed, and writes each run's model steps to
// <out>/seed<N>.jsonl for `gotogether validate`. A run that does not
// settle (the stampSpec mutant's storm) is said so on stderr, its steps
// written all the same: validate reports the cycle they go round. A
// mutant's is built with its overlay (run-demo.sh's MUTANT=stampSpec).
//
//	requeue-sim -seeds 20 -out /tmp/runs
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/chucklehead-dev/gotogether/examples/requeue"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
)

func main() {
	seeds := flag.Int("seeds", 20, "runs, with seeds 1..N")
	rate := flag.Float64("fault-rate", 0.2, "probability of each message fault")
	out := flag.String("out", "runs", "directory for the step logs")
	flag.Parse()
	if err := os.MkdirAll(*out, 0o755); err != nil {
		fail(err)
	}
	unsettled := 0
	for seed := 1; seed <= *seeds; seed++ {
		sink, err := qobs.FileSink(filepath.Join(*out, fmt.Sprintf("seed%d.jsonl", seed)), qobs.PerStep)
		if err != nil {
			fail(err)
		}
		c := qenv.NewSeeded(uint64(seed))
		c.Rates = qenv.Rates{Default: *rate}
		rec := qobs.NewRecorder(sink, qobs.WithActor(fmt.Sprintf("seed%d", seed)))
		err = requeue.Run(qenv.WithContext(c, qobs.WithRecorder(context.Background(), rec)))
		if errors.Is(err, qenv.ErrUnsettled) {
			unsettled++
		} else if err != nil {
			fail(err)
		}
		if err := rec.Close(); err != nil { // closes the step log
			fail(err)
		}
	}
	if unsettled > 0 {
		fmt.Fprintf(os.Stderr, "requeue-sim: %d of %d runs did not settle: the controller was still requeueing a minute into the settle\n", unsettled, *seeds)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "requeue-sim:", err)
	os.Exit(2)
}
