package main

import (
	"bytes"
	"os"
	"regexp"
	"strings"
	"testing"
)

// Every command's -h says what it is for and gives an example; a removed
// flag (usage "gone: ...") is not listed. Each flag set in the source is
// one of commands, so none is left with Go's bare "Usage of x:".
func TestCommandHelp(t *testing.T) {
	var src []byte
	for _, f := range []string{"main.go", "cover.go", "evidence.go", "simulate.go"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src = append(src, b...)
	}
	if bare := regexp.MustCompile(`flag\.NewFlagSet\(`).FindAll(src, -1); len(bare) > 0 {
		t.Errorf("%d flag sets made without commandFlags: their -h has no purpose or example", len(bare))
	}
	for _, m := range regexp.MustCompile(`commandFlags\("([^"]+)"\)`).FindAllSubmatch(src, -1) {
		name := string(m[1])
		fset := commandFlags(name)
		fset.Bool("old", false, "gone: use -new")
		var b bytes.Buffer
		fset.SetOutput(&b)
		fset.Usage()
		c := commands[name]
		if !strings.Contains(b.String(), c.purpose) || !strings.Contains(b.String(), c.example) || strings.Contains(b.String(), "-old") {
			t.Errorf("%s -h:\n%s", name, b.String())
		}
	}
}
