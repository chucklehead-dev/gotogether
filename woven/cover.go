package woven

import "github.com/chucklehead-dev/gotogether/internal/wovenrt"

// A Block is a straight-line run of statements, as qweave -cover
// registers it; its ID is assigned by RegisterBlocks.
type Block = wovenrt.Block

// RegisterBlocks adds a file's blocks and returns the ID of the first:
// woven code numbers its blocks from it. It is called from package
// variable initialization, before any block runs.
func RegisterBlocks(bs []Block) int { return wovenrt.RegisterBlocks(bs) }

// Hit records that block id ran: woven code calls it on entry to each
// block. The run that collects it is the calling code's (qcover.WithCounts).
func Hit(id int) { wovenrt.Hit(id) }
