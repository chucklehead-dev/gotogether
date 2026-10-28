package library

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/chucklehead-dev/gotogether/grist"
	"github.com/chucklehead-dev/gotogether/grist/gristtest"
	"github.com/chucklehead-dev/gotogether/grist/memstore"
)

// history is the workload a log is recorded from: members joining,
// books catalogued (one refused: no copies), borrows (one refused: no
// copy free, one: no member) and a return. No node runs: the borrows'
// receipts are left for the version that reads the log.
var history = []struct {
	name string
	args any
}{
	{"join", Join{Member: "a"}}, {"join", Join{Member: "b"}},
	{"add-book", AddBook{Book: "x", Copies: 2}}, {"add-book", AddBook{Book: "y", Copies: 1}}, {"add-book", AddBook{Book: "z", Copies: 0}},
	{"borrow", Borrow{Member: "a", Book: "x"}}, {"borrow", Borrow{Member: "b", Book: "x"}}, {"borrow", Borrow{Member: "a", Book: "y"}},
	{"borrow", Borrow{Member: "b", Book: "y"}}, {"return-book", ReturnBook{Member: "a", Book: "x"}}, {"borrow", Borrow{Member: "c", Book: "x"}},
	{"add-book", AddBook{Book: "x", Copies: 1}},
}

// TestRecordLog records the history with the app as it is now into the
// file LIBRARY_RECORD_LOG names (gristtest.Capture, WriteLog): a log of
// the deployed version, kept as testdata so that every later version is
// judged on it (TestOldLogConforms). testdata/logs/v1.jsonl was recorded
// so before book-added's version 2; never record over it.
func TestRecordLog(t *testing.T) {
	path := os.Getenv("LIBRARY_RECORD_LOG")
	if path == "" {
		t.Skip("LIBRARY_RECORD_LOG=<file> records the history with the app as it is now")
	}
	ctx := context.Background()
	st := gristtest.Capture(memstore.New(App.Name))
	if err := st.CreateTenant(ctx, "t1"); err != nil {
		t.Fatal(err)
	}
	for i, c := range history {
		args, _ := json.Marshal(c.args)
		_, err := App.Execute(ctx, st, grist.Envelope{ID: fmt.Sprintf("old-%d", i+1), Tenant: "t1", Caller: "librarian"}, c.name, args)
		var rej *grist.Rejected
		if err != nil && !errorsAs(err, &rej) {
			t.Fatalf("%s %s: %v", c.name, args, err)
		}
	}
	if err := gristtest.WriteLog(st.Log(), path); err != nil {
		t.Fatal(err)
	}
}

func errorsAs(err error, target any) bool { return errors.As(err, target) }
