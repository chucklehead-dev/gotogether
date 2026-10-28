// Command auditsteps writes the payment handler's audit logs as a step log,
// which `go tool gotogether validate -binding payments.binding.yaml` judges:
//
//	go run ./cmd/auditsteps -out steps.jsonl testdata/logs/payments-2026-10-02.jsonl
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/chucklehead-dev/gotogether/examples/migrate-payments/auditsteps"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

func main() {
	out := flag.String("out", "-", "step log to write (- for stdout)")
	flag.Parse()
	var all []qtrace.Step
	for _, p := range flag.Args() {
		steps, err := auditsteps.ReadFile(p)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		all = append(all, steps...)
	}
	w := os.Stdout
	if *out != "-" {
		f, err := os.Create(*out)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer f.Close()
		w = f
	}
	if err := qtrace.WriteJSONL(w, all); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "%d steps\n", len(all))
}
