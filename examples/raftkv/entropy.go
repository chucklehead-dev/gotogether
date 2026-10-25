package raftkv

import (
	"context"
	"crypto/rand"
	"io"
	"slices"
	"sync"

	"github.com/chucklehead-dev/gotogether/qenv"
)

// raft randomizes each election timeout with crypto/rand (its globalRand,
// raft.go), which no Config option replaces: a simulation that left it so
// would not replay. electionEntropy takes crypto/rand.Reader's place
// process-wide, once: read from a task of a node's process, it draws
// each byte from the simulation ("raft.election"); read anywhere else (a
// recorder's id) it is the real reader.
var electionOnce sync.Once // process-wide: the reader is installed once

func installElectionEntropy() {
	electionOnce.Do(func() { rand.Reader = electionEntropy{real: rand.Reader} })
}

type electionEntropy struct{ real io.Reader }

// Read draws a byte per byte asked. raft asks for a timeout below its
// election ticks (Intn), which crypto/rand.Int reads as one byte and
// accepts below the bound: the draw is of ten values, and each node's is
// offset by three, so the nominal choice (0 everywhere, Hegel's shrinking
// target) still gives the nodes different timeouts and one wins its
// election instead of every one splitting the vote forever.
func (e electionEntropy) Read(b []byte) (int, error) {
	ctx := context.Background() // qenv finds the task by its goroutine
	s, p := qenv.From(ctx), qenv.ProcessOf(ctx)
	if s == nil || p == nil {
		return e.real.Read(b)
	}
	i := slices.Index(Nodes, p.Name)
	if i < 0 {
		return e.real.Read(b)
	}
	for k := range b {
		b[k] = byte((s.Choose("raft.election", 10) + 3*i) % 10)
	}
	return len(b), nil
}
