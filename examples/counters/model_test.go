package counters

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/chucklehead-dev/gotogether/qtest"
)

// The model on its own proves its invariants within a bound: a grist
// model's log grows without end, so its state graph is never complete,
// and a state bound alone would be a breadth-first prefix proving
// nothing. A depth bound makes it every state within three commands of
// init, a bounded proof the claim says so (simulate.search.depth, here
// on a copy of the generated binding, which says nothing of a bound).
func TestModelProvesWithinDepth(t *testing.T) {
	qtest.Model(t, depthBound(t, "counters.binding.yaml", 3)).Expect(t, qtest.Proves("uniqueNames"), qtest.Proves("averageAsOfTrigger"))
}

// depthBound is the generated binding b as gotogether.binding/2 with
// simulate.search.depth d, written beside a copy of the model.
func depthBound(t *testing.T, b string, d int) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.CopyFS(filepath.Join(dir, "model"), os.DirFS("model")); err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(b)
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)
	if strings.Count(text, "\nsimulate:\n") != 1 {
		t.Fatalf("%s has no simulate section", b)
	}
	text = "format: gotogether.binding/2\n" + strings.Replace(text, "\nsimulate:\n", "\nsimulate:\n  search: {depth: "+strconv.Itoa(d)+"}\n", 1)
	p := filepath.Join(dir, b)
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}
