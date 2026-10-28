package simfix

import (
	"context"
	"time"

	"github.com/chucklehead-dev/gotogether/qenv"
)

func work() {}

// With a context in scope, quintsim's findings carry their fix.
func task(ctx context.Context) {
	go work()                   // want "goroutine started with `go`"
	_ = time.Now()              // want `time.Now reads the wall clock`
	time.Sleep(time.Second)     // want `time.Sleep reads the wall clock`
	go func(n int) { _ = n }(1) // want "goroutine started with `go`"
	func() { _ = time.Now() }() // want `time.Now reads the wall clock`
	_ = qenv.Nominal{}
}

// Without one, no fix: there is no context to give.
func noContext() {
	_ = time.Now() // want `time.Now reads the wall clock`
}
