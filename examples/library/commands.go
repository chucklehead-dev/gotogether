package library

import "github.com/chucklehead-dev/gotogether/grist"

// Join makes Member a member.
type Join struct {
	Member string `json:"member"`
}

// AddBook adds Copies copies of Book to the catalogue.
type AddBook struct {
	Book   string `json:"book"`
	Copies int    `json:"copies"`
}

// Borrow lends Member one copy of Book.
type Borrow struct {
	Member string `json:"member"`
	Book   string `json:"book"`
}

// ReturnBook gives back one copy of Book that Member holds.
type ReturnBook struct {
	Member string `json:"member"`
	Book   string `json:"book"`
}

// The refusals.
var (
	ErrAlreadyMember = grist.Rejection("already-member")
	ErrInvalid       = grist.Rejection("invalid")
	ErrNotMember     = grist.Rejection("not-member")
	ErrUnavailable   = grist.Rejection("unavailable")
	ErrNotHeld       = grist.Rejection("not-held")
)

// decideJoin refuses a member who already joined: its read of the member's
// join is in the read set, so two joins of one name commit once.
func decideJoin(r *grist.Reads, c Join) ([]grist.Event, error) {
	if r.Exists(Joined, grist.Tag("member", c.Member)) {
		return nil, ErrAlreadyMember
	}
	return []grist.Event{Joined.Of(MemberJoined{Member: c.Member})}, nil
}

// decideAddBook refuses a number of copies that is not positive, before
// reading anything.
func decideAddBook(r *grist.Reads, c AddBook) ([]grist.Event, error) {
	if c.Copies <= 0 {
		return nil, ErrInvalid
	}
	return []grist.Event{Added.Of(BookAdded{Book: c.Book, Count: c.Copies, Shelf: MainShelf})}, nil
}

// decideBorrow refuses a stranger and a book with no copy free, in that
// order. Each check reads what it depends on (the member's join, the
// book's shelf), so the append commits only if neither changed since: two
// borrows of a book's last copy commit once. The loan limit is a rule on
// every borrow (LoanLimit, rules.go), checked after the decision.
func decideBorrow(r *grist.Reads, c Borrow) ([]grist.Event, error) {
	if !r.Exists(Joined, grist.Tag("member", c.Member)) {
		return nil, ErrNotMember
	}
	if Books.For(r, c.Book).Free() <= 0 {
		return nil, ErrUnavailable
	}
	return []grist.Event{Borrowed.Of(BookBorrowed{Member: c.Member, Book: c.Book})}, nil
}

// decideReturn refuses a book the member does not hold (a stranger holds
// none).
func decideReturn(r *grist.Reads, c ReturnBook) ([]grist.Event, error) {
	for _, b := range Loans.For(r, c.Member) {
		if b == c.Book {
			return []grist.Event{Returned.Of(BookReturned{Member: c.Member, Book: c.Book})}, nil
		}
	}
	return nil, ErrNotHeld
}

// The commands.
var (
	JoinCmd   = grist.NewCommand(App, "join", decideJoin, grist.Refuses(ErrAlreadyMember), grist.Authorize(grist.Authenticated))
	AddCmd    = grist.NewCommand(App, "add-book", decideAddBook, grist.Refuses(ErrInvalid), grist.Authorize(grist.Authenticated))
	BorrowCmd = grist.NewCommand(App, "borrow", decideBorrow, grist.Appends(Borrowed), grist.Refuses(ErrNotMember, ErrUnavailable), grist.Authorize(grist.Authenticated))
	ReturnCmd = grist.NewCommand(App, "return-book", decideReturn, grist.Refuses(ErrNotHeld), grist.Authorize(grist.Authenticated))
)
