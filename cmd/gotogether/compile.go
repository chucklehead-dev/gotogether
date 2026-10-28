package main

import (
	"fmt"
	"io"

	"github.com/chucklehead-dev/gotogether/validate"
)

// compileTo is the compile command: the binding's model compiled for the
// Go evaluator (validate.Compile), written to -o, for validate -compiled.
func compileTo(out io.Writer, args []string) error {
	fset := commandFlags("compile")
	b := fset.String("binding", "", "the binding whose model to compile")
	o := fset.String("o", "", "the compiled model's file (gotogether.model/1 JSON)")
	quint := fset.String("quint", "quint", "quint binary")
	fset.Parse(args)
	if *b == "" || *o == "" || fset.NArg() > 0 {
		return needs("compile", "-binding and -o, and no other argument")
	}
	cm, err := validate.Compile(*b, validate.Options{Quint: *quint})
	if err != nil {
		return err
	}
	if err := cm.Write(*o); err != nil {
		return err
	}
	fmt.Fprintf(out, "compiled %s's model %s (quint %s, %d sources) to %s: gotogether validate -binding %s -compiled %s FILES judges runs with no quint\n",
		*b, cm.Module, cm.Quint, len(cm.Sources), *o, *b, *o)
	return nil
}
