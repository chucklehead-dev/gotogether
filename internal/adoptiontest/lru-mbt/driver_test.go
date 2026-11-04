package lrumbt

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"testing"

	"github.com/chucklehead-dev/gotogether/connect"
	"github.com/hashicorp/golang-lru/v2/simplelru"
)

// callback is a comparable public observation, not an expected eviction.
// The count preserves duplicate callbacks without assuming their order.
type callback struct {
	Key   string
	Value int
}

type observed struct {
	Keys      []string
	Values    map[string]int
	Count     int
	Callbacks map[callback]int
	Found     bool
	Value     int
	Changed   bool
	Evicted   bool
	Removed   int
}

// cacheDriver owns one real cache per model initialization. It reads picks,
// invokes the published API, and reports actual observations; it contains no
// predicted cache order, capacity, membership or eviction classifier.
type cacheDriver struct {
	cache     *simplelru.LRU[string, int]
	callbacks map[callback]int
	found     bool
	value     int
	changed   bool
	evicted   bool
	removed   int
}

func (d *cacheDriver) Step(s *connect.Step) error {
	d.callbacks = map[callback]int{}
	d.found, d.value, d.changed, d.evicted, d.removed = false, 0, false, false, 0
	if s.Action == "Init" {
		cache, err := simplelru.NewLRU[string, int](2, func(key string, value int) { d.callbacks[callback{key, value}]++ })
		if err != nil {
			return fmt.Errorf("initialize published LRU: %w", err)
		}
		d.cache = cache
		return nil
	}
	if d.cache == nil {
		return fmt.Errorf("%s before Init", s.Action)
	}
	return (connect.Switch{
		"Add": func(s *connect.Step) error {
			d.evicted = d.cache.Add(connect.MustPick[string](s, "key"), connect.MustPick[int](s, "value"))
			return nil
		},
		"Get": func(s *connect.Step) error {
			d.value, d.found = d.cache.Get(connect.MustPick[string](s, "key"))
			return nil
		},
		"Peek": func(s *connect.Step) error {
			d.value, d.found = d.cache.Peek(connect.MustPick[string](s, "key"))
			return nil
		},
		"Remove": func(s *connect.Step) error {
			d.changed = d.cache.Remove(connect.MustPick[string](s, "key"))
			return nil
		},
		"Resize": func(s *connect.Step) error { d.removed = d.cache.Resize(connect.MustPick[int](s, "size")); return nil },
	}).Step(s)
}

func (d *cacheDriver) State() (any, error) {
	if d.cache == nil {
		return nil, fmt.Errorf("state before Init")
	}
	keys := d.cache.Keys()
	values := make(map[string]int, len(keys))
	for _, key := range keys {
		value, ok := d.cache.Peek(key)
		if !ok {
			return nil, fmt.Errorf("published Keys includes absent key %q", key)
		}
		values[key] = value
	}
	return observed{Keys: keys, Values: values, Count: d.cache.Len(), Callbacks: maps.Clone(d.callbacks), Found: d.found, Value: d.value, Changed: d.changed, Evicted: d.evicted, Removed: d.removed}, nil
}

func modelConfig(t *testing.T) connect.Config {
	t.Helper()
	cfg := connect.Config{Spec: "model/lru.qnt", Seed: "17", MaxSamples: 32, MaxSteps: 24, NondetPath: []string{"last"}, StatePath: []string{"observed"}}
	if base := os.Getenv("LRU_EVIDENCE_DIR"); base != "" {
		cfg.Dir = filepath.Join(base, t.Name())
	}
	return cfg
}

func TestSampledLRU(t *testing.T) {
	cfg := modelConfig(t)
	cfg.Args = []string{"--invariant", "coherent"}
	connect.Run(t, cfg, func() connect.Driver { return &cacheDriver{} })
}

func TestForcedLRU(t *testing.T) {
	for _, name := range []string{
		"mbtPeekDoesNotPromoteTest", "mbtGetPromotesTest", "mbtResizeEvictsOldestTest",
		"mbtUpdateAndRemoveTest", "mbtMissesAreNonmutatingTest",
	} {
		t.Run(name, func(t *testing.T) {
			connect.Test(t, modelConfig(t), name, func() connect.Driver { return &cacheDriver{} })
		})
	}
}
