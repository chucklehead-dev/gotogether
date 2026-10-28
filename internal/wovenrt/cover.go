package wovenrt

import (
	"sync"

	"github.com/chucklehead-dev/gotogether/internal/runkeys"
	"github.com/chucklehead-dev/gotogether/qrun"
)

// A Block is a straight-line run of statements, as woven code registers
// it (qcover.Block is its public form).
type Block struct {
	File      string
	Line, Col int // start
	EndLine   int
	Func      string // the enclosing function, as pkg.Func or pkg.(T).Method
	Stmts     int
	ID        int
}

// Process-wide: the blocks woven code registers at init.
var (
	blockMu sync.Mutex
	blocks  []Block
)

// RegisterBlocks adds a file's blocks and returns the ID of the first.
func RegisterBlocks(bs []Block) int {
	blockMu.Lock()
	defer blockMu.Unlock()
	base := len(blocks)
	for i, b := range bs {
		b.ID = base + i
		blocks = append(blocks, b)
	}
	return base
}

// Blocks are all registered blocks, by ID.
func Blocks() []Block {
	blockMu.Lock()
	defer blockMu.Unlock()
	return append([]Block(nil), blocks...)
}

// Coverage is the run's collector of block hits, with the task that ran
// each (qcover.WithCounts puts its Counts' here).
var Coverage = qrun.NewKey[func(id int, task any)]("coverage")

// Hit records that block id ran, in the calling code's run.
func Hit(id int) {
	rc, _ := qrun.Current()
	hit, _ := qrun.InContext(rc, Coverage)
	if hit == nil {
		return
	}
	task, _ := qrun.InContext(rc, runkeys.Task)
	hit(id, task)
}
