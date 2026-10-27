// Package poskey writes a position as a key segment that sorts in
// position order, for stores that key rows by position
// (t/<tenant>/e/<position>).
package poskey

import (
	"fmt"
	"strconv"

	"github.com/chucklehead-dev/gotogether/grist/store"
)

// Of is p as a 20-digit key segment.
func Of(p store.Position) string { return fmt.Sprintf("%020d", p) }

// Parse is the position a 20-digit key segment names.
func Parse(s string) (store.Position, error) {
	n, err := strconv.ParseInt(s, 10, 64)
	return store.Position(n), err
}
