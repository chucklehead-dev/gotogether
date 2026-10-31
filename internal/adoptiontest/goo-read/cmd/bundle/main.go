// Command bundle makes a captured manifest self-contained before relocation.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/chucklehead-dev/gotogether/validate"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: bundle MANIFEST DIRECTORY")
		os.Exit(2)
	}
	manifest, err := validate.ReadManifest(os.Args[1])
	if err == nil {
		err = manifest.Bundle(filepath.Join(os.Args[2], "run"))
	}
	if err == nil {
		err = manifest.Write(filepath.Join(os.Args[2], "manifest.json"))
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}
