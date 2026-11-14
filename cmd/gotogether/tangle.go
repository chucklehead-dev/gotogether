package main

import (
	"fmt"
	"io"

	"github.com/chucklehead-dev/gotogether/internal/literate"
)

// tangle is the tangle command: each literate model (a Markdown document
// whose ```quint blocks are the model, ROADMAP CW1) written to its .qnt
// beside it, as every binding naming the document does when it loads.
// With -check it writes nothing and fails (exit 1) when a .qnt there is
// missing or stale (generated from another version of its document): the
// check CI runs where the generated model is checked in.
func tangle(out io.Writer, args []string) (int, error) {
	fset := commandFlags("tangle")
	check := fset.Bool("check", false, "write nothing: fail if a document's generated .qnt is missing or stale")
	fset.Parse(args)
	if fset.NArg() == 0 {
		return 2, needs("tangle", "one or more Markdown documents")
	}
	code := 0
	for _, doc := range fset.Args() {
		if !literate.IsDoc(doc) {
			return 2, fmt.Errorf("tangle: %s is no Markdown document (.md): name the document whose ```quint blocks are the model", doc)
		}
		if *check {
			if err := literate.Check(doc); err != nil {
				fmt.Fprintf(out, "FAIL %v\n", err)
				code = 1
				continue
			}
			fmt.Fprintf(out, "ok   %s: %s is current\n", doc, literate.GeneratedPath(doc))
			continue
		}
		t, gen, err := literate.Write(doc)
		if err != nil {
			return 2, err
		}
		fmt.Fprintf(out, "%s: %s, %s → %s\n", doc, plural(t.Blocks, "quint block"), plural(len(t.Lines)-3, "line"), gen)
	}
	return code, nil
}
