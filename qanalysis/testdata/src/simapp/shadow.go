package simapp

import (
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
)

type fakeClock struct{}

func (fakeClock) Now() time.Time { return time.Time{} }

// A local named time is no import: its Now is the fake's (the CLI's
// syntactic check, without types, reports it).
func shadowed(s *qenv.Sim) time.Time {
	time := fakeClock{}
	return time.Now()
}
