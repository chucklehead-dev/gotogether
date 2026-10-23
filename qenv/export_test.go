package qenv

// BreakValidation makes st commit every transaction, whatever its level
// says: a broken store, for the history check to catch.
func BreakValidation(st *Store) { st.noValidation = true }
