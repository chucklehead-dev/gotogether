// Command actionmap prints the code each model action statically reaches
// from where its steps are recorded (qanalysis/actionmap) and, with
// -coverage, compares it with the code its steps ran: an exploration's
// evidence, which explorations woven with qweave -cover write under
// $GOTOGETHER_EVIDENCE (or a qcover.Summary as JSON, which examples/checkout's
// TestCodeCoverage writes). It lists dead functions
// first: the ones no root (main, init, exported API) reaches.
//
//	actionmap [-C dir] [-coverage EVIDENCE] packages...
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/chucklehead-dev/gotogether/qanalysis/actionmap"
	"github.com/chucklehead-dev/gotogether/qcover"
)

func main() {
	dir := flag.String("C", ".", "directory to load the packages from")
	cov := flag.String("coverage", "", "an evidence.json or qcover.Summary JSON to compare with")
	flag.Parse()
	if flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: actionmap [-C dir] [-coverage evidence.json] packages...")
		os.Exit(2)
	}
	m, err := actionmap.Static(*dir, flag.Args()...)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, f := range m.Dead {
		fmt.Printf("dead: %s (no root reaches it)\n", f)
	}
	if *cov == "" {
		for _, a := range m.Actions() {
			fmt.Printf("%s: sites %v, reaches %d functions\n", a, m.Sites[a], len(m.Reach[a]))
		}
		return
	}
	s, err := qcover.ReadSummary(*cov)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Print(actionmap.Report(actionmap.Compare(m, s)))
}
