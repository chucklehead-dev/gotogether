package library

import "github.com/chucklehead-dev/gotogether/grist"

// MemberLoans asks for a member's current loans.
type MemberLoans struct {
	Member string `json:"member"`
}

// LoansView is a member's loans as shown: how many, and the books held in
// the order borrowed (a book once per copy held). The model compares a
// result's scalar fields only (Count): the list says so (grist:"-").
type LoansView struct {
	Count int      `json:"count"`
	Books []string `json:"books" grist:"-"`
}

// LoansQuery is the member-loans query: the member's loans through the position it
// read.
var LoansQuery = grist.NewQuery(App, "member-loans", func(r *grist.Reads, q MemberLoans) (LoansView, error) {
	held := Loans.For(r, q.Member)
	return LoansView{Count: len(held), Books: append([]string{}, held...)}, nil
}, grist.Authorize(grist.Authenticated))
