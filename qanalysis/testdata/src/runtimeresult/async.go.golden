package runtimeresult

import "time"

// No direct qenv import: the typed check still knows the aliased receiver.
func asynchronousAlias(s *Simulation) error {
	go s.Settle(time.Second) // want "discarded qenv.Sim.Settle runtime error"
	return nil
}
