package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/chucklehead-dev/gotogether/validate"
)

// compileTo is the compile command: the binding's model compiled for the
// Go evaluator (validate.Compile), written to -o (by default beside the
// binding, validate.CompiledFor), for validate -compiled and for qtest's
// tests without quint. With -check it writes nothing: it compiles again and
// fails if the file there differs in meaning, the check CI runs where quint
// is installed so that a checked-in compiled model cannot go stale.
func compileTo(out io.Writer, args []string) error {
	fset := commandFlags("compile")
	b := fset.String("binding", "", "the binding whose model to compile")
	o := fset.String("o", "", "the compiled model's file (gotogether.model/1 JSON; default: beside the binding, <stem>.model.json)")
	check := fset.Bool("check", false, "write nothing: compile again and fail if the file at -o differs in meaning")
	quint := fset.String("quint", "quint", "quint binary")
	fset.Parse(args)
	if *b == "" || fset.NArg() > 0 {
		return needs("compile", "-binding, and no other argument")
	}
	if *o == "" {
		*o = validate.CompiledFor(*b)
	}
	cm, err := validate.Compile(*b, validate.Options{Quint: *quint})
	if err != nil {
		return err
	}
	if *check {
		have, err := validate.LoadCompiled(*o)
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("compile -check: %s has no compiled model at %s: compile it (gotogether compile -binding %s -o %s) and check it in", *b, *o, *b, *o)
		}
		if err != nil {
			return fmt.Errorf("compile -check: %w", err)
		}
		if d := cm.Differs(have); len(d) > 0 {
			return fmt.Errorf("compile -check: %s is stale for %s: %s differ from a compile now (quint %s): compile it again (gotogether compile -binding %s -o %s) and check it in",
				*o, *b, strings.Join(d, ", "), cm.Quint, *b, *o)
		}
		fmt.Fprintf(out, "%s is current for %s (model %s, quint %s, %d sources)\n", *o, *b, cm.Module, cm.Quint, len(cm.Sources))
		return nil
	}
	if err := cm.Write(*o); err != nil {
		return err
	}
	fmt.Fprintf(out, "compiled %s's model %s (quint %s, %d sources) to %s: gotogether validate -binding %s -compiled %s FILES judges runs with no quint, and qtest uses it where quint is not installed\n",
		*b, cm.Module, cm.Quint, len(cm.Sources), *o, *b, *o)
	return nil
}
