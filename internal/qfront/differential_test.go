package qfront_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"testing"

	"github.com/chucklehead-dev/gotogether/internal/qoracle"
	"github.com/chucklehead-dev/gotogether/internal/skip"
)

// The Go parse against quint's, on fixtures that exercise every
// alternative of Quint.g4 the parser has and the parse-phase errors (P1's
// push tier; the whole corpus is the oracle's, GOTOGETHER_QFRONT_STUB=go
// GOTOGETHER_QFRONT_CORPUS=1 go test -run TestCorpus ./internal/qoracle/).
// Each fixture is compared twice: in the P1 tier by canonical form
// (qoracle.P1, canon), and by its raw ids, where the Go parse must number
// every node, and place it, as quint does (the source maps equal, id for
// id).
func TestParseAgreesWithQuint(t *testing.T) {
	if testing.Short() {
		t.Skip("runs quint parse on each fixture")
	}
	skip.Quint(t)
	files, err := filepath.Glob("testdata/*.qnt")
	if err != nil {
		t.Fatal(err)
	}
	errs, _ := filepath.Glob("testdata/errors/*.qnt")
	files = append(files, errs...)
	if len(files) < 2 {
		t.Fatalf("no fixtures: %v", files)
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			t.Parallel()
			abs, err := filepath.Abs(f)
			if err != nil {
				t.Fatal(err)
			}
			p := qoracle.Program{ID: f, File: abs}
			oracle := qoracle.ParseTier{FrontEnd: qoracle.Quint{ParseOnly: true}}
			oq, og, err := qoracle.RunBoth(t.Context(), p, oracle, qoracle.GoParse{}, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			d, err := qoracle.CompareOutputs(oq, og)
			if err != nil {
				t.Fatal(err)
			}
			if d != nil {
				t.Fatalf("quint and the Go parse disagree: %v", d)
			}
			if oq.SourceMap == nil {
				return // a syntax error: its first error compared, nothing else
			}
			if diff := rawSpans(t, oq, og); diff != "" {
				t.Errorf("the Go parse numbers nodes otherwise than quint: %s", diff)
			}
		})
	}
}

// rawSpans compares two source maps by their raw ids: each id's file
// (relative to its run's root), start and end.
func rawSpans(t *testing.T, a, b *qoracle.Output) string {
	read := func(o *qoracle.Output) map[string]string {
		var sm struct {
			SourceIndex map[string]string            `json:"sourceIndex"`
			Map         map[string][]json.RawMessage `json:"map"`
		}
		if err := json.Unmarshal(o.SourceMap, &sm); err != nil {
			t.Fatal(err)
		}
		out := map[string]string{}
		for id, e := range sm.Map {
			var idx string
			json.Unmarshal(e[0], &idx)
			if idx == "" {
				idx = string(e[0])
			}
			rel, _ := filepath.Rel(o.Root, sm.SourceIndex[idx])
			out[id] = fmt.Sprintf("%s %s %s", rel, compact(e[1]), compact(e[2]))
		}
		return out
	}
	ma, mb := read(a), read(b)
	var ids []string
	for id := range ma {
		ids = append(ids, id)
	}
	for id := range mb {
		if _, ok := ma[id]; !ok {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	n := 0
	var first string
	for _, id := range ids {
		if ma[id] != mb[id] {
			if n == 0 {
				first = fmt.Sprintf("id %s: quint %q, go %q", id, ma[id], mb[id])
			}
			n++
		}
	}
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("%d of %d ids differ; the first: %s", n, len(ids), first)
}

func compact(b []byte) string {
	var x bytes.Buffer
	json.Compact(&x, b)
	return x.String()
}
