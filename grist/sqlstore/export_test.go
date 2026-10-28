package sqlstore

import "context"

// Classify is classify, for the tests.
var Classify = classify

// WaitFor is s's wait on done, for its test.
func WaitFor(s *Store, ctx context.Context, done func() (bool, error)) error {
	return s.wait(ctx, done)
}

// Committed counts a COMMIT of s's, as an append's does.
func Committed(s *Store) { s.commits.Add(1) }

// ReadStmt is the statement a read runs per type, for its plan's test.
const ReadStmt = readStmt
