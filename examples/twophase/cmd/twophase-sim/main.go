// Command twophase-sim runs two-phase commit in simulated environments,
// one per seed, and writes each run's model steps to <out>/seed<N>.jsonl
// for `gotogether validate` against the Choreo model.
//
//	twophase-sim -seeds 20 -mutation impatient -out /tmp/runs
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/chucklehead-dev/gotogether/examples/twophase"
	"github.com/chucklehead-dev/gotogether/examples/twophase/tpc"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
)

func main() {
	seeds := flag.Int("seeds", 20, "runs, with seeds 1..N")
	mut := flag.String("mutation", "", "early-commit | impatient | volatile-decision")
	rate := flag.Float64("fault-rate", 0.2, "probability of each message fault")
	crash := flag.Float64("crash-rate", 0.05, "probability of a crash at each scheduling step")
	out := flag.String("out", "runs", "directory for the step logs")
	flag.Parse()
	m, ok := map[string]tpc.Mutation{"": tpc.Faithful, "early-commit": tpc.EarlyCommit, "impatient": tpc.ImpatientParticipant,
		"volatile-decision": tpc.VolatileDecision}[*mut]
	if !ok {
		fail(fmt.Errorf("unknown mutation %q", *mut))
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		fail(err)
	}
	var unsettledSeeds []string
	for seed := 1; seed <= *seeds; seed++ {
		// durable: a crash is part of the run, and the log must keep its
		// last steps
		sink, err := qobs.FileSink(filepath.Join(*out, fmt.Sprintf("seed%d.jsonl", seed)), qobs.PerStep)
		if err != nil {
			fail(err)
		}
		c := qenv.NewSeeded(uint64(seed))
		c.Rates = qenv.Rates{Default: *rate, Kinds: map[string]float64{"nemesis": *crash}}
		rec := qobs.NewRecorder(sink, qobs.WithActor(fmt.Sprintf("seed%d", seed)))
		_, err = twophase.Run(qenv.WithContext(c, qobs.WithRecorder(context.Background(), rec)), twophase.Config{Mutation: m})
		var unsettled *qenv.Unsettled
		if errors.As(err, &unsettled) {
			// a run that does not settle is a verdict on the run, not a
			// failure of the tool: early-commit's coordinator resends a
			// commit forever to a participant that voted to abort. Its log
			// has no settle record, so validate judges its steps (the commit
			// the model refuses) and no eventual property.
			unsettledSeeds = append(unsettledSeeds, fmt.Sprintf("%d (%s)", seed, strings.Join(unsettled.Tasks, "; ")))
		} else if err != nil {
			fail(err)
		}
		if err := rec.Close(); err != nil { // closes the step log
			fail(err)
		}
	}
	if len(unsettledSeeds) > 0 {
		fmt.Fprintf(os.Stderr, "twophase-sim: %d of %d runs did not settle (their logs have no settle record: validate judges their steps, no eventual property): seed %s\n",
			len(unsettledSeeds), *seeds, strings.Join(unsettledSeeds, ", seed "))
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "twophase-sim:", err)
	os.Exit(2)
}
