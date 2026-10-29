package simgrist

import (
	"time"

	"github.com/chucklehead-dev/gotogether/grist"
)

var _ = grist.AtLeastOnce

// stamp is an application's code reading the wall clock.
func stamp() int64 {
	return time.Now().UnixMilli() // want `time.Now reads the wall clock in a grist application, which gristsim runs in virtual time and no model can compute: use the event's Meta.Time`
}
