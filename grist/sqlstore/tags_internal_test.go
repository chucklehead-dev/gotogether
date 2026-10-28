package sqlstore

import (
	"encoding/json"
	"math/rand/v2"
	"reflect"
	"testing"

	"github.com/chucklehead-dev/gotogether/grist/store"
)

// decodeTags reads what json.Unmarshal reads wherever it answers, and
// answers for json.Marshal's output of tags that need no escape: checked
// on random tags of plain, quoted, escaped, control, HTML and invalid
// UTF-8 characters, marshalled, then cut and mutated byte by byte.
func TestDecodeTagsAgainstJSON(t *testing.T) {
	r := rand.New(rand.NewPCG(9, 10))
	alphabet := []string{"a", "k", "=", "/", " ", "\"", "\\", "\n", "<", "é", "\xff", " ", ",", "[", "]"}
	str := func() string {
		var s string
		for range r.IntN(4) {
			s += alphabet[r.IntN(len(alphabet))]
		}
		return s
	}
	answered := 0
	for range 20000 {
		pairs := make([][2]string, r.IntN(4))
		for i := range pairs {
			pairs[i] = [2]string{str(), str()}
		}
		b, err := json.Marshal(pairs)
		if err != nil {
			t.Fatal(err)
		}
		in := string(b)
		switch r.IntN(4) {
		case 0:
			in = in[:r.IntN(len(in)+1)]
		case 1:
			bs := []byte(in)
			bs[r.IntN(len(bs))] = "[],\" a\\\x01"[r.IntN(8)]
			in = string(bs)
		}
		got, ok := decodeTags(in)
		if !ok {
			continue
		}
		answered++
		var kvs [][2]string
		if err := json.Unmarshal([]byte(in), &kvs); err != nil {
			t.Fatalf("decodeTags(%q) = %v, but json.Unmarshal fails: %v", in, got, err)
		}
		var want []store.TagPair
		for _, kv := range kvs {
			want = append(want, store.TagPair{Key: kv[0], Value: kv[1]})
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("decodeTags(%q) = %#v, json.Unmarshal %#v", in, got, want)
		}
	}
	if answered < 2000 {
		t.Fatalf("decodeTags answered %d of 20000: too few to test it", answered)
	}
	// the tags an append writes, of plain keys and values, take the fast path
	if got, ok := decodeTags(`[["acct","1"],["kind","x y"]]`); !ok || len(got) != 2 {
		t.Fatalf("decodeTags of plain tags: %v %v, want two pairs", got, ok)
	}
}
