package qhistory_test

import (
	"fmt"

	"github.com/chucklehead-dev/gotogether/qhistory"
)

func ExampleCheck_sessionOrder() {
	// The client awaited each attempt before invoking the next. These
	// ordinals assert that evidence; global transaction IDs cannot replace it.
	history := []qhistory.Txn{
		{ID: 42, Session: "client-a", SessionOrder: 1, Outcome: qhistory.Committed,
			Ops: []qhistory.Op{{Kind: qhistory.Append, Key: "items", Value: 7}}},
		{ID: 9, Session: "client-a", SessionOrder: 2, Outcome: qhistory.Committed,
			Ops: []qhistory.Op{{Kind: qhistory.Read, Key: "items", List: []int{}}}},
	}
	report, err := qhistory.Check(qhistory.StrongSessionSerializable, history)
	if err != nil {
		panic(err)
	}
	fmt.Println(report.OK()) // the later read missed the client's own commit
	// Output: false
}
