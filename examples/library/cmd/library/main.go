// Command library is the library application as a binary (grist.Main):
// `library model` writes its model's skeleton and its bindings from its
// catalog, `library serve -db sqlite:library.db -tenant t1 -steps
// steps.jsonl` serves its HTTP API and runs its node on a SQLite file,
// recording every step for validation with its bindings.
package main

import (
	_ "modernc.org/sqlite" // serve's driver: -db sqlite:PATH

	library "github.com/chucklehead-dev/gotogether/examples/library"
	"github.com/chucklehead-dev/gotogether/grist"
)

func main() { grist.Main(library.App) }
