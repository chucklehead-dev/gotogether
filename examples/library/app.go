// Package library is a lending library on grist: members join, books are
// catalogued with a number of copies, members borrow (at most Limit loans
// each, never more copies than the book has) and return them, and every
// borrow's receipt is mailed at least once through a mailer that
// deduplicates. Its model is model/library.qnt (the rules) beside the
// skeleton grist writes from the App's catalog (model/library_gen.qnt: `go
// run ./cmd/library model`); its tests explore it in a simulation
// (sim_test.go), catch its mutants (mutants_test.go) and judge its real tier
// on SQLite (real_test.go), all with the same generated bindings.
package library

import "github.com/chucklehead-dev/gotogether/grist"

// The events. A borrow and a return carry both the member's tag and the
// book's, so one event feeds both keyed read models (each key a tag of every
// handler's type).
type (
	// MemberJoined: Member joined the library.
	MemberJoined struct {
		Member string `json:"member" grist:"tag"`
	}
	// BookAdded: Copies more copies of Book in the catalogue.
	BookAdded struct {
		Book   string `json:"book" grist:"tag"`
		Copies int    `json:"copies"`
	}
	// BookBorrowed: Member took one copy of Book.
	BookBorrowed struct {
		Member string `json:"member" grist:"tag"`
		Book   string `json:"book" grist:"tag"`
	}
	// BookReturned: Member gave one copy of Book back.
	BookReturned struct {
		Member string `json:"member" grist:"tag"`
		Book   string `json:"book" grist:"tag"`
	}
)

// App is the application; its blocks are below and in commands.go,
// queries.go and processors.go.
var App = grist.New("library")

// The event types.
var (
	Joined   = grist.NewEvent[MemberJoined](App, "member-joined")
	Added    = grist.NewEvent[BookAdded](App, "book-added")
	Borrowed = grist.NewEvent[BookBorrowed](App, "book-borrowed")
	Returned = grist.NewEvent[BookReturned](App, "book-returned")
)

// Shelf is a book's copies and how many of them are on loan.
type Shelf struct {
	Copies int
	OnLoan int
}

// Free is how many copies are on the shelf.
func (s Shelf) Free() int { return s.Copies - s.OnLoan }

// Books is each book's shelf, keyed by book: a borrow of b reads, and is
// conditioned on, b's additions, borrows and returns alone.
var Books = grist.NewKeyedReadModel(App, "books", 1, grist.Keys("book"), func() Shelf { return Shelf{} },
	grist.On(Added, func(s Shelf, e BookAdded, m grist.Meta) Shelf { s.Copies += e.Copies; return s }),
	grist.On(Borrowed, func(s Shelf, e BookBorrowed, m grist.Meta) Shelf { s.OnLoan++; return s }),
	grist.On(Returned, func(s Shelf, e BookReturned, m grist.Meta) Shelf { s.OnLoan--; return s }))

// Loans is each member's current loans, keyed by member: the books held, in
// the order borrowed, a book once per copy held.
var Loans = grist.NewKeyedReadModel(App, "loans", 1, grist.Keys("member"), func() []string { return nil },
	grist.On(Borrowed, func(s []string, e BookBorrowed, m grist.Meta) []string {
		return append(append([]string(nil), s...), e.Book)
	}),
	grist.On(Returned, func(s []string, e BookReturned, m grist.Meta) []string { return without(s, e.Book) }))

// without is s with b's first occurrence removed (a copy, not s itself: a
// read model's state is never mutated in place).
func without(s []string, b string) []string {
	for i, x := range s {
		if x == b {
			return append(append([]string(nil), s[:i]...), s[i+1:]...)
		}
	}
	return s
}

// Limit is how many loans a member may hold at once. The model states the
// same (LIMIT).
const Limit = 3
