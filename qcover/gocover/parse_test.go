package gocover_test

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qcover/gocover"
)

// Parse merges a block seen twice, and reports a malformed count with its
// cause wrapped.
func TestParse(t *testing.T) {
	p, err := gocover.Parse(strings.NewReader("mode: set\na/b.go:1.2,3.4 2 1\na/b.go:1.2,3.4 2 0\na/c.go:5.1,6.1 1 0\n"))
	if err != nil || len(p.Blocks) != 2 || p.Blocks[0].Count != 1 || p.Ratio() != 2.0/3 {
		t.Fatalf("%+v %v", p, err)
	}
	_, err = gocover.Parse(strings.NewReader("a/b.go:1.2,3.4 2 many\n"))
	if err == nil || err.Error() != `gocover: line 1: strconv.Atoi: parsing "many": invalid syntax` || !errors.Is(err, strconv.ErrSyntax) {
		t.Errorf("a malformed count: %v", err)
	}
}
