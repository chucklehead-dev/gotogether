package main

import (
	"context"
	"encoding/json"
	membership "example.com/tail-membership"
	"flag"
	"fmt"
	"os"
)

func main() {
	cases := flag.String("cases", "testdata/cases.json", "authored operation sequence")
	out := flag.String("out", "steps.jsonl", "native observations")
	results := flag.String("results", "observations.json", "complete public responses")
	flag.Parse()
	if err := run(*cases, *out, *results); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}
func run(casesPath, out, results string) error {
	cases, err := membership.LoadCases(casesPath)
	if err != nil {
		return err
	}
	steps, observations, err := membership.Observe(context.Background(), cases)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(observations, "", "  ")
	if err != nil {
		return err
	}
	if err = os.WriteFile(results, append(data, '\n'), 0600); err != nil {
		return err
	}
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	err = membership.WriteTrace(f, steps)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	return err
}
