package main

import (
	"fmt"
	"strings"

	"github.com/chucklehead-dev/gotogether/qcover"
	"github.com/chucklehead-dev/gotogether/qcover/gocover"
)

// runCover reads a real environment's coverage (GOCOVERDIRs of binaries
// built with -cover) and, given a simulation's coverage summary, what ran
// in one and not the other.
func runCover(args []string) error {
	fset := commandFlags("cover")
	covdir := fset.String("covdir", "", "comma-separated GOCOVERDIRs of the real environment's runs")
	sim := fset.String("sim", "", "a simulation's coverage: an exploration's evidence (under GOTOGETHER_EVIDENCE) or a qcover.Summary as JSON")
	fset.Parse(args)
	if *covdir == "" {
		return fmt.Errorf("cover needs -covdir")
	}
	real, err := gocover.Read(strings.Split(*covdir, ",")...)
	if err != nil {
		return err
	}
	fmt.Printf("real environment: %.1f%% of statements in %d blocks\n", 100*real.Ratio(), len(real.Blocks))
	if *sim == "" {
		return nil
	}
	s, err := qcover.ReadSummary(*sim)
	if err != nil {
		return err
	}
	fmt.Print(gocover.Compare(s, real))
	return nil
}
