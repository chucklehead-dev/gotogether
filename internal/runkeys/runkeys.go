// Package runkeys holds the core's own qrun keys, kept out of the public
// API: the state a run carries between the core's packages.
package runkeys

import "github.com/chucklehead-dev/gotogether/qrun"

// Task identifies the simulation task a context belongs to (qenv sets it
// on every task's context): state kept per task, such as the code a task
// ran since its last step (qcover).
var Task = qrun.NewKey[any]("task")
