package nilshadow

import (
	"errors"
	env "github.com/chucklehead-dev/gotogether/qenv"
)

func work(s *env.Sim) error {
	s.Run(1) // want "discarded qenv.Sim.Run runtime error"
	return nil
}

var nil = errors.New("package-level nil is visible before its declaration")
