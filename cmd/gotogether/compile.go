package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/chucklehead-dev/gotogether/internal/mbtmodel"
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
	spec := fset.String("spec", "", "instead of -binding: the spec whose main module to compile for connect's traces (gotogether.mbt/1: connect.Run, Bind's Run and GenerateRun with no quint, the Go evaluator simulating it)")
	main := fset.String("main", "", "with -spec: the main module, as connect.Config.Main (default: the file's)")
	o := fset.String("o", "", "the compiled model's file (gotogether.model/1 JSON, gzipped where the name ends in .gz; default: beside the binding, <stem>.model.json.gz; with -spec, gotogether.mbt/1 beside the spec, <stem>.mbt.json or <stem>.<main>.mbt.json)")
	check := fset.Bool("check", false, "write nothing: compile again and fail if the file at -o differs in meaning")
	quint := fset.String("quint", "quint", "quint binary")
	fset.Parse(args)
	if *spec != "" {
		if *b != "" || fset.NArg() > 0 {
			return needs("compile", "-binding or -spec, not both, and no other argument")
		}
		return compileMBT(out, *spec, *main, *o, *quint, *check)
	}
	if *main != "" {
		return needs("compile", "-main with -spec")
	}
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

// compileMBT is compile -spec: the spec's main module compiled for
// connect (validate.CompileMBT), written to o (default
// connect.CompiledFor), or, with check, compared with the file there.
func compileMBT(out io.Writer, spec, main, o, quint string, check bool) error {
	if o == "" {
		o = mbtmodel.CompiledFor(spec, main)
	}
	cm, err := validate.CompileMBT(context.Background(), validate.MBTOptions{Spec: spec, Main: main, Quint: quint}, o)
	if err != nil {
		return err
	}
	again := "gotogether compile -spec " + spec
	if main != "" {
		again += " -main " + main
	}
	if check {
		have, _, err := mbtmodel.Load(o)
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("compile -check: %s has no compiled model at %s: compile it (%s -o %s) and check it in", spec, o, again, o)
		}
		if err != nil {
			return fmt.Errorf("compile -check: %w", err)
		}
		if d := cm.Differs(have); len(d) > 0 {
			return fmt.Errorf("compile -check: %s is stale for %s: %s differ from a compile now (quint %s): compile it again (%s -o %s) and check it in",
				o, spec, strings.Join(d, ", "), cm.Quint, again, o)
		}
		fmt.Fprintf(out, "%s is current for %s (quint %s, %d sources)\n", o, spec, cm.Quint, len(cm.Sources))
		return nil
	}
	if err := cm.Write(o); err != nil {
		return err
	}
	fmt.Fprintf(out, "compiled %s for connect (quint %s, %d sources) to %s: connect.Run generates its traces from it with the Go evaluator where quint is not installed (GOTOGETHER_COMPILED)\n",
		spec, cm.Quint, len(cm.Sources), o)
	return nil
}
