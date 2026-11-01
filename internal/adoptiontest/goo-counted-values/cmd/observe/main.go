package main

import (
	"context"
	"encoding/json"
	counted "example.com/counted-values"
	"flag"
	"fmt"
	"os"
)

func main() {
	casesPath := flag.String("cases", "testdata/cases.json", "source response cases")
	out := flag.String("out", "steps.jsonl", "native observed step log")
	results := flag.String("results", "observations.json", "actual public responses")
	flag.Parse()
	if err := run(*casesPath, *out, *results); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}
func run(casesPath, out, results string) error {
	cases, err := counted.LoadCases(casesPath)
	if err != nil {
		return err
	}
	steps, observations, err := counted.Observe(context.Background(), cases)
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(observations, "", "  ")
	if err != nil {
		return err
	}
	if err = os.WriteFile(results, append(b, '\n'), 0600); err != nil {
		return err
	}
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	err = counted.WriteTrace(f, steps)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	return err
}
