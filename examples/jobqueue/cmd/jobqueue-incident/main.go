// Command jobqueue-incident records a bounded stale-lease incident and its
// faithful and repaired controls through the actual jobqueue service.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/chucklehead-dev/gotogether/examples/jobqueue/queue"
	"github.com/chucklehead-dev/gotogether/qenv"
	"github.com/chucklehead-dev/gotogether/qobs"
)

type scenario struct {
	name     string
	mutation queue.Mutation
	complete bool
}

// scenarios is the read-only set of recorded controls and the planted incident.
var scenarios = []scenario{
	{name: "faithful", mutation: queue.Faithful},
	{name: "incident", mutation: queue.AckAnyLease},
	{name: "repaired", mutation: queue.Faithful, complete: true},
}

func main() {
	if err := run(os.Args[1:], os.Stderr); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, "jobqueue-incident:", err)
		os.Exit(2)
	}
}

func run(args []string, stderr io.Writer) error {
	fs := flag.NewFlagSet("jobqueue-incident", flag.ContinueOnError)
	fs.SetOutput(stderr)
	out := fs.String("out", "incident-traces", "directory for faithful, incident and repaired native step logs")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected arguments %v: use -out DIR", fs.Args())
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		return err
	}
	// Refuse a mixed evidence directory before recording any case. The
	// repaired control is a fresh execution, never an edit of the incident.
	for _, c := range scenarios {
		path := filepath.Join(*out, c.name+".jsonl")
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("%s already exists: choose a fresh -out directory", path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	for _, c := range scenarios {
		path := filepath.Join(*out, c.name+".jsonl")
		if err := record(path, c); err != nil {
			return fmt.Errorf("%s: %w", c.name, err)
		}
		fmt.Fprintln(stderr, "recorded", path)
	}
	return nil
}

func record(path string, c scenario) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	sink := qobs.NewJSONLSink(file, qobs.PerStep)
	rec := qobs.NewRecorder(sink, qobs.WithID("jobqueue-incident-"+c.name), qobs.WithActor("jobqueue"))
	ctx := qobs.WithRecorder(context.Background(), rec)
	chooser := qenv.NewSeeded(1)
	chooser.Rates = qenv.NoFaults
	sim := qenv.New(qenv.WithContext(chooser, ctx))
	defer sim.Close()
	var scenarioErr error
	sim.Spawn("stale-lease", func(ctx context.Context) {
		svc := queue.New(queue.Config{LeaseTTL: time.Second, Now: func() time.Time { return qenv.Now(ctx) }, Mutation: c.mutation})
		if err := svc.Enqueue(ctx, 1); err != nil {
			scenarioErr = fmt.Errorf("enqueue: %w", err)
			return
		}
		job, ok := svc.Lease(ctx, "w1")
		if !ok || job != 1 {
			scenarioErr = fmt.Errorf("lease returned job %d, leased=%v: expected job 1", job, ok)
			return
		}
		svc.Restart() // Actual process incarnation changes; the binding infers restart.
		err := (&queue.Worker{Name: "w1", Svc: svc}).Once(ctx, job)
		want := "pending"
		if c.mutation == queue.AckAnyLease {
			want = "done"
			if err != nil {
				scenarioErr = fmt.Errorf("incident no longer accepts the stale ack: %w", err)
				return
			}
		} else if !errors.Is(err, queue.ErrStaleLease) {
			scenarioErr = fmt.Errorf("control ack error %v, want stale lease", err)
			return
		}
		if got := svc.Status()[job]; got != want {
			scenarioErr = fmt.Errorf("job status %q, want %q", got, want)
			return
		}
		if c.complete {
			// The repair must still admit a current lease: always refusing
			// acknowledgements would hide the incident without doing the job.
			fresh, ok := svc.Lease(ctx, "w1")
			if !ok || fresh != job {
				scenarioErr = fmt.Errorf("fresh lease returned job %d, leased=%v: expected job %d", fresh, ok, job)
				return
			}
			if err := (&queue.Worker{Name: "w1", Svc: svc}).Once(ctx, fresh); err != nil {
				scenarioErr = fmt.Errorf("fresh lease acknowledgement: %w", err)
				return
			}
			if got := svc.Status()[job]; got != "done" {
				scenarioErr = fmt.Errorf("repaired job status %q, want done", got)
			}
		}
	})
	runErr := sim.Run(time.Second)
	closeErr := errors.Join(rec.Close(), file.Close())
	if runErr != nil {
		return runErr
	}
	if scenarioErr != nil {
		return scenarioErr
	}
	wantSteps := 4
	if c.complete {
		wantSteps = 7
	}
	if rec.Count() != wantSteps {
		return fmt.Errorf("recorded %d steps, want %d", rec.Count(), wantSteps)
	}
	return closeErr
}
