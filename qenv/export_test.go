package qenv

// BreakValidation makes st commit every transaction, whatever its level
// says: a broken store, for the history check to catch.
func BreakValidation(st *Store) { st.noValidation = true }

// BreakQueue makes q forget the messages a crashed consumer held: a
// broken queue, for the contract check to catch.
func BreakQueue(q *Queue) { q.forget = true }

// TermContexts is how many contexts Terminating gave p's incarnation
// that the signal would cancel.
func TermContexts(p *Process) int { return len(p.termCancels) }
