package library

import "github.com/chucklehead-dev/gotogether/grist"

// MainShelf is the shelf a book is catalogued on until add-book takes
// one: every addition of version 1, and every one add-book appends.
const MainShelf = "main"

// BookAddedV1 is book-added as version 1 stored it: Copies more copies
// of Book. Version 2 renamed copies to count and added the shelf; the
// events stored at version 1 stay as they are, and every read upcasts
// them (upcastBookAdded), so no fold, rule or handler sees this struct.
type BookAddedV1 struct {
	Book   string `json:"book" grist:"tag"`
	Copies int    `json:"copies"`
}

// upcastBookAdded is book-added's version 1 as version 2: its copies the
// count, on the main shelf. Its tag (the book) is kept: the store indexed
// the event by it.
var upcastBookAdded = grist.Upcast(1, func(v BookAddedV1) BookAdded {
	return BookAdded{Book: v.Book, Shelf: MainShelf} // mutant: version 1's copies dropped on the way to the count
})
