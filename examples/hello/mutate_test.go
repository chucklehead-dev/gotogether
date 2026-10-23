package hello

import (
	"testing"

	"github.com/chucklehead-dev/gotogether/qtest"
)

// Every mutant of the counter and its client, woven by qweave -mutate,
// killed by an exploration or marked equivalent with the reason:
//
//	go run -C ../../qweave ./cmd/qweave -C ../examples/hello -mutate \
//	  -coverpkg github.com/chucklehead-dev/gotogether/examples/hello go test -run TestMutationOfTheCounter .
//
// Without the client's done step a client that sent two requests of
// three survived: nothing said the client finished. Unwoven, it skips.
// The three marked mutants are equivalent under this environment.
func TestMutationOfTheCounter(t *testing.T) {
	qtest.RequireQuint(t)
	// besides conforming, every run ends with the client done: a mutant
	// that loses the step (or the requests it needs) is killed by that
	done := qtest.Takes("done").InAtLeast(0.9)
	system(Faithful).Mutate(t, qtest.ExploreConfig{Seeds: 30, Rates: lossy}, done).Expect(t, qtest.NoSurvivors(
		"hello.go:42", // the replay step unrecorded: a replay changes nothing the model sees
		"hello.go:55", // one more retry: the run differs only if ten attempts all fail
		"hello.go:79", // the run's error check negated: it differs only if the client gave up
	))
}
