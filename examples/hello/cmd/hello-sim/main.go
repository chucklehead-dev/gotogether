// Command hello-sim runs the counter and its client in simulated
// environments, one per seed, and writes each run's model steps to
// <out>/seed<N>.jsonl for `gotogether validate`. A mutant's is built
// with its overlay (run-demo.sh's MUTANT=noDedupe).
//
//	hello-sim -seeds 20 -out /tmp/runs
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/chucklehead-dev/gotogether/examples/hello"
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
	for seed := 1; seed <= *seeds; seed++ {
		sink, err := qobs.FileSink(filepath.Join(*out, fmt.Sprintf("seed%d.jsonl", seed)), qobs.PerStep)
		if err != nil {
			fail(err)
		}
		c := qenv.NewSeeded(uint64(seed))
		c.Rates = qenv.Rates{Default: *rate}
		rec := qobs.NewRecorder(sink, qobs.WithActor(fmt.Sprintf("seed%d", seed)))
		if err := hello.Run(qenv.WithContext(c, qobs.WithRecorder(context.Background(), rec))); err != nil {
			fail(err)
		}
		if err := rec.Close(); err != nil { // closes the step log
			fail(err)
		}
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "hello-sim:", err)
	os.Exit(2)
}
