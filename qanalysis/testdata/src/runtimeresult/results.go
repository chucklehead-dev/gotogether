package runtimeresult

import (
	"errors"
	"time"

	env "github.com/chucklehead-dev/gotogether/qenv"
)

type Simulation = env.Sim
type errorAlias = error

func namedAlias(s *env.Sim) (err errorAlias) {
	s.Run(time.Second) // want "discarded qenv.Sim.Run runtime error"
	return nil
}

func clauseBodies(s *env.Sim) error {
	switch {
	case true:
		s.Settle(time.Second) // want "discarded qenv.Sim.Settle runtime error"
	}
	select {
	default:
		s.Run(time.Second) // want "discarded qenv.Sim.Run runtime error"
	}
	return nil
}

func run(s *env.Sim) error {
	s.Run(time.Second) // want "discarded qenv.Sim.Run runtime error"
	return nil
}

func settle(s *env.Sim, p *env.Process) error {
	_ = s.SettleCore(time.Second, p) // want "discarded qenv.Sim.SettleCore runtime error"
	return nil
}

func until(s *env.Sim) error {
	s.RunUntil(time.Second /* preserve the predicate explanation */, func() bool { return true }) // want "discarded qenv.Sim.RunUntil runtime error"
	_, _ = s.RunUntil(time.Second, func() bool { return false })                                  // want "discarded qenv.Sim.RunUntil runtime error"
	return nil
}

func parenthesized(s *env.Sim) error {
	(s.Settle)(time.Second) // want "discarded qenv.Sim.Settle runtime error"
	return nil
}

func methodExpression(s *env.Sim) error {
	(*env.Sim).Run(s, time.Second) // want "discarded qenv.Sim.Run runtime error"
	return nil
}

func closure(s *env.Sim) error {
	f := func() error {
		s.Settle(time.Second) // want "discarded qenv.Sim.Settle runtime error"
		return nil
	}
	return f()
}

func closureCannotReturnOuterError(s *env.Sim) error {
	f := func() {
		s.Settle(time.Second) // want "discarded qenv.Sim.Settle runtime error"
	}
	f()
	return nil
}

func retainedBoolean(s *env.Sim) error {
	done, _ := s.RunUntil(time.Second, func() bool { return true }) // want "discarded qenv.Sim.RunUntil runtime error"
	_ = done
	return nil
}

func noReturn(s *env.Sim) {
	s.Settle(time.Second) // want "discarded qenv.Sim.Settle runtime error"
}

func multipleReturns(s *env.Sim) (int, error) {
	s.Settle(time.Second) // want "discarded qenv.Sim.Settle runtime error"
	return 1, nil
}

func asynchronous(s *env.Sim) error {
	go s.Settle(time.Second) //qenv:ok source fixture isolates discarded errors
	defer s.Run(time.Second) // want "discarded qenv.Sim.Run runtime error"
	return nil
}

func header(s *env.Sim) error {
	if _ = s.Settle(time.Second); true { // want "discarded qenv.Sim.Settle runtime error"
		return nil
	}
	return nil
}

func post(s *env.Sim) error {
	for ; false; _ = s.Run(time.Second) { // want "discarded qenv.Sim.Run runtime error"
	}
	return nil
}

func nilShadow(s *env.Sim) error {
	nil := errors.New("not the predeclared nil")
	s.Run(time.Second) // want "discarded qenv.Sim.Run runtime error"
	return nil
}

func handled(s *env.Sim) error { return s.Settle(time.Second) }

type custom struct{}

func (*custom) Settle(time.Duration) error { return nil }

func unrelated(c *custom) error {
	c.Settle(time.Second)
	return nil
}
