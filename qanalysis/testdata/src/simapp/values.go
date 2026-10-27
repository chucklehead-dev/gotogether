package simapp

import (
	"math/rand/v2"
	. "time"

	"github.com/chucklehead-dev/gotogether/qenv"
)

var _ qenv.Nominal

type ticker struct{ now func() Time }

// Function values escape as their calls do, through a dot import too; a
// seeded generator's methods do not.
func values() {
	now := Now            // want `time.Now used as a value: a call of it reads the wall clock`
	c := ticker{now: Now} // want `time.Now used as a value`
	draw := rand.IntN     // want `math/rand/v2.IntN used as a value`
	Sleep(Millisecond)    // want `time.Sleep reads the wall clock`
	g := rand.New(rand.NewPCG(1, 2))
	pick := g.IntN
	_, _, _, _ = now, c, draw, pick
}
