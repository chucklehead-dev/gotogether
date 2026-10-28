package wovenrt

import (
	"fmt"
	"os"
	"strconv"
	"sync"

	"github.com/chucklehead-dev/gotogether/qrun"
)

// A Mutant is one woven change, as woven code registers it (qmut.Mutant
// is its public form).
type Mutant struct {
	ID   int
	File string
	Line int
	Func string
	// Op is the change: "negate if", "<= for <" and the like, "drop call".
	Op string
	// Code is the original code, for reports.
	Code string
}

// Process-wide: the mutants woven code registers at init.
var (
	mutantMu sync.Mutex
	mutants  []Mutant
)

// RegisterMutants adds a file's mutants and returns the ID of the first.
// With GOTOGETHER_MUTANT=list each is printed to standard error as it
// registers ("qmut: mutant 3 file.go:12 f: ...").
func RegisterMutants(ms []Mutant) int {
	mutantMu.Lock()
	defer mutantMu.Unlock()
	base := len(mutants)
	for i, m := range ms {
		m.ID = base + i
		mutants = append(mutants, m)
		if os.Getenv("GOTOGETHER_MUTANT") == "list" {
			fmt.Fprintf(os.Stderr, "qmut: mutant %d %s:%d %s: %s (%s)\n", m.ID, m.File, m.Line, m.Func, m.Op, m.Code)
		}
	}
	return base
}

// Mutants are all registered mutants, by ID.
func Mutants() []Mutant {
	mutantMu.Lock()
	defer mutantMu.Unlock()
	return append([]Mutant(nil), mutants...)
}

// process is the mutant GOTOGETHER_MUTANT=<id> turns on for the whole
// process (-1: none): plain unit tests, which carry no run's context,
// run one mutant a process (the toolkit's own mutation testing, a test
// binary run once per mutant).
// Process-wide.
var process = func() int {
	n, err := strconv.Atoi(os.Getenv("GOTOGETHER_MUTANT"))
	if err != nil {
		return -1
	}
	return n
}()

// Active is the run's mutant, its ID plus one (0: none; qmut.WithMutant).
var Active = qrun.NewKey[int]("mutant")

// Reach notes that a mutant's code ran, on or off (qmut.WithReach).
var Reach = qrun.NewKey[func(id int)]("mutant reach")

// MutantOn reports whether mutant id is the calling code's run's mutant,
// and notes that its code was reached.
func MutantOn(id int) bool {
	if process >= 0 {
		// the process's mutant, its runs' reach untracked: the run lookup
		// calls the resolver qenv installs, which, mutated itself when
		// qenv is, would call MutantOn again
		return id == process
	}
	rc, _ := qrun.Current()
	if note, ok := qrun.InContext(rc, Reach); ok && note != nil {
		note(id)
	}
	v, _ := qrun.InContext(rc, Active)
	return v == id+1
}
