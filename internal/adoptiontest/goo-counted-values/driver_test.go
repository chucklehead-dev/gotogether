package counted

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"testing"

	"github.com/chucklehead-dev/gotogether/input"
	"github.com/chucklehead-dev/gotogether/qtrace"
)

// This is a representation gate, not the classification oracle. State is the
// executor's actual output and is judged independently by the Quint model.
func TestExactCountNativeRoundTrip(t *testing.T) {
	cases, err := LoadCases("testdata/cases.json")
	if err != nil {
		t.Fatal(err)
	}
	steps, observations, err := Observe(t.Context(), cases)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := WriteTrace(&buf, steps); err != nil {
		t.Fatal(err)
	}
	decoded, err := qtrace.ReadJSONL(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded) != len(cases) {
		t.Fatalf("read %d cases, want %d", len(decoded), len(cases))
	}
	for i, c := range cases {
		actual := observations[i]
		if actual.Reads != 2 || actual.Result.Count != c.Total {
			t.Fatalf("%s lost public count or read boundary: %+v", c.Name, actual)
		}
		counts := make([]string, len(actual.Result.Groups))
		for j, g := range actual.Result.Groups {
			counts[j] = g.Count
		}
		if !reflect.DeepEqual(counts, c.Groups) {
			t.Fatalf("%s public groups=%v want%v", c.Name, counts, c.Groups)
		}
		s := decoded[i]
		if fmt.Sprint(s.Args["total"]) != c.Total || fmt.Sprint(s.Obs["count"]) != actual.Result.Count || s.Obs["rawCount"] != actual.Result.Count || s.Obs["state"] != actual.Result.State {
			t.Fatalf("%s changed scalar observations: %+v", c.Name, s)
		}
		var nums []json.Number
		decoder := json.NewDecoder(bytes.NewBufferString(s.Args["groups"].(string)))
		decoder.UseNumber()
		if err := decoder.Decode(&nums); err != nil {
			t.Fatal(err)
		}
		got := make([]string, len(nums))
		for j, n := range nums {
			got[j] = n.String()
		}
		if !reflect.DeepEqual(got, c.Groups) {
			t.Fatalf("%s changed argument integers: %v", c.Name, got)
		}
		if s.Obs["groups"] != steps[i].Obs["groups"] || s.Obs["rawGroups"] != stringList(counts) {
			t.Fatalf("%s changed collection observations: %+v", c.Name, s.Obs)
		}
	}
	// The ordinary high-level reader must consume exactly the bytes the native
	// writer produced too; a special test decoder is insufficient evidence.
	path := t.TempDir() + "/steps.jsonl"
	if err := os.WriteFile(path, buf.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	read, err := input.Read(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(read.Steps) != len(cases) {
		t.Fatalf("input.Read lost steps: %d", len(read.Steps))
	}
	for i, c := range cases {
		if fmt.Sprint(read.Steps[i].Obs["count"]) != c.Total {
			t.Fatalf("input.Read rounded %s", c.Name)
		}
	}
}
