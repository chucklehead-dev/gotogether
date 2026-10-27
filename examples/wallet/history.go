package wallet

// accountLists reads the bank's store as qhistory's list-append workload
// sees it: each account a list of entry tags (a read the list it
// returned, a write the tag it appended). qhistory.Record records every
// attempt (Retry's included) with its outcome, and qhistory.Check judges
// the history against an isolation level, as it would a real database's
// (Elle's method).
func accountLists(key string, val any) (string, []int, bool) {
	a, ok := isAccount(key)
	if !ok {
		return "", nil, false
	}
	es, _ := val.([]Entry)
	return a, tags(es), true
}
