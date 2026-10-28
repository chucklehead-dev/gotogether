package library

import "github.com/chucklehead-dev/gotogether/grist"

// ErrLimit is LoanLimit's refusal: the member holds Limit loans.
var ErrLimit = grist.Rejection("limit")

// LoanLimit refuses a borrow by a member who holds Limit loans: a rule on
// the event, not a check in the borrow's decision, so every command that
// appends a borrow is held to it (borrow declares it appends one:
// grist.Appends). It reads the member's loans on the borrow's own reads,
// so its append commits only if they did not change since: two borrows of
// a member's last loan commit once. Its refusal is the borrow's.
var LoanLimit = grist.NewRule(App, "loan-limit", Borrowed, func(r *grist.Reads, e BookBorrowed) error {
	if len(Loans.For(r, e.Member)) >= Limit {
		return ErrLimit
	}
	return nil
}, grist.Refuses(ErrLimit))
