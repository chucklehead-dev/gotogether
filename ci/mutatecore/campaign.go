package main

import (
	"context"
	"fmt"
	"sync"
)

// runCampaign records each completed source before reporting progress. Only a
// fully resolved, successfully recorded campaign can return a complete result.
// Errors cancel remaining work; cancellation is never a killed mutation.
func runCampaign(parent context.Context, candidates []candidate, jobs int, gate gatePolicy, trial func(context.Context, target) (executionResult, error), j *journal, progress func(int)) ([]bool, error) {
	ctx, cancel := context.WithCancelCause(parent)
	defer cancel(nil)
	tasks := make(chan int, len(candidates))
	for i := range candidates {
		tasks <- i
	}
	close(tasks)
	results := make([]bool, len(candidates))
	var wg sync.WaitGroup
	var mu sync.Mutex
	completed := 0
	for range jobs {
		wg.Go(func() {
			for {
				if ctx.Err() != nil {
					return
				}
				i, ok := <-tasks
				if !ok {
					return
				}
				c := candidates[i]
				record := outcomeRecord{Source: sourceMetadata(c)}
				passed, err := survives(c, func(t target) (bool, error) {
					execution, err := trial(ctx, t)
					if err == nil {
						err = execution.validate()
					}
					passed := execution.passed()
					attempt := attemptRecord{targetRecord: targetRecord{t.suite, t.id}, Outcome: "passed", Execution: execution}
					if err != nil {
						attempt.Outcome = "error"
						attempt.Error = err.Error()
					} else if !passed {
						attempt.Outcome = "failed"
					}
					record.Attempts = append(record.Attempts, attempt)
					return passed, err
				})
				// Even if the subprocess raced cancellation and exited successfully,
				// interrupted work cannot be reported as a resolved source outcome.
				if err == nil && ctx.Err() != nil {
					err = context.Cause(ctx)
				}
				record.Outcome = "killed"
				if err != nil {
					record.Outcome = "error"
					record.Error = err.Error()
				} else if passed {
					record.Outcome = "survived"
				}
				if logErr := j.append("outcome", record); logErr != nil {
					cancel(logErr)
					return
				}
				if err != nil {
					cancel(err)
					return
				}
				results[i] = passed
				mu.Lock()
				completed++
				progress(completed)
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	if completed != len(candidates) {
		return nil, fmt.Errorf("only %d of %d source outcomes resolved", completed, len(candidates))
	}
	survivors := 0
	for _, survived := range results {
		if survived {
			survivors++
		}
	}
	if err := j.append("complete", completeRecord{Total: len(candidates), Survivors: survivors, Gate: gate.verdict(survivors)}); err != nil {
		return nil, err
	}
	return results, nil
}
