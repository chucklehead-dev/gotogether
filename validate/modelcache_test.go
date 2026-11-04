package validate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/skip"
)

// The model cache is by content: the same files load once, and a spec
// or an import changed at the same path is loaded anew (a stale model
// would judge the old one).
func TestModelCacheByContent(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	dir := t.TempDir()
	spec := filepath.Join(dir, "m.qnt")
	write(t, dir, map[string]string{
		"lib.qnt": "module lib {\n  pure def two = 2\n}\n",
		"m.qnt":   "module m {\n  import lib.* from \"./lib\"\n  var x: int\n  action init = x' = two\n  action step = x' = x + 1\n}\n",
	})
	a, err := loadModel(t.Context(), "quint", spec, "m")
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := loadModel(t.Context(), "quint", spec, "m"); b != a {
		t.Error("the same files loaded twice")
	}
	write(t, dir, map[string]string{"m.qnt": "module m {\n  import lib.* from \"./lib\"\n  var x: int\n  action init = x' = two\n  action step = x' = x + 1\n  action reset = x' = 0\n}\n"})
	c, err := loadModel(t.Context(), "quint", spec, "m")
	if _, ok := c.Actions["reset"]; err != nil || c == a || !ok {
		t.Errorf("a changed spec: %v, actions %v", err, c.ActionNames())
	}
	os.WriteFile(filepath.Join(dir, "lib.qnt"), []byte("module lib {\n  pure def two = 3\n}\n"), 0o644)
	if d, err := loadModel(t.Context(), "quint", spec, "m"); err != nil || d == c {
		t.Errorf("a changed import gave the cached model: %v", err)
	}
}

// The cache follows every shape of import quint reads: an instance's
// with a string constant, one split over lines, transitively. A change
// to a file only such an import reaches loads the model anew.
func TestModelCacheFollowsEveryImport(t *testing.T) {
	t.Parallel()
	skip.Quint(t)
	dir := t.TempDir()
	spec := filepath.Join(dir, "m.qnt")
	write(t, dir, map[string]string{
		"base.qnt":  "module base {\n  pure def one = 1\n}\n",
		"inst.qnt":  "module inst {\n  const N: Set[str]\n  import base.* from \"./base\"\n  pure def two = one + 1\n}\n",
		"split.qnt": "module split {\n  pure def three = 3\n}\n",
		"m.qnt": "module m {\n  import inst(N = Set(\"u\")) as I from \"./inst\"\n  import split.*\n    from\n    \"./split\"\n" +
			"  var x: int\n  action init = x' = I::two + three\n  action step = x' = x + 1\n}\n",
	})
	a, err := loadModel(t.Context(), "quint", spec, "m")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ file, src string }{
		{"split.qnt", "module split {\n  pure def three = 4\n}\n"},
		{"inst.qnt", "module inst {\n  const N: Set[str]\n  import base.* from \"./base\"\n  pure def two = one + 2\n}\n"},
		{"base.qnt", "module base {\n  pure def one = 2\n}\n"},
	} {
		write(t, dir, map[string]string{c.file: c.src})
		b, err := loadModel(t.Context(), "quint", spec, "m")
		if err != nil || b == a {
			t.Errorf("%s changed, the cached model: %v", c.file, err)
		}
		a = b
	}
}

// A canceled caller must neither launch Quint nor leave a failed memo entry
// that prevents a later caller from loading the same unchanged model.
func TestModelCacheCanceledCallerCanRetry(t *testing.T) {
	skip.Quint(t)
	t.Setenv("GOTOGETHER_CACHE", "off")
	dir := t.TempDir()
	spec := filepath.Join(dir, "m.qnt")
	write(t, dir, map[string]string{"m.qnt": "module m { var x: int\n action init = x' = 0\n action step = x' = x + 1\n}"})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := loadModel(ctx, "quint", spec, "m"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled load: %v", err)
	}
	m, err := loadModel(t.Context(), "quint", spec, "m")
	if err != nil || m == nil {
		t.Fatalf("fresh caller after cancellation: %v", err)
	}
	if _, err := loadModel(ctx, "quint", spec, "m"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled cache hit: %v", err)
	}
}
