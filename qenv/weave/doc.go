// Package weave holds the aspects (orchestrion.yml) that bring code using
// plain Go concurrency (sync.Mutex, sync.RWMutex, sync.WaitGroup and the
// go statement) into a qenv simulation without changing its source. Built
// with Orchestrion or qweave, with this package imported in the project's
// orchestrion.tool.go, the method calls of the root module's packages
// become calls of package woven (the woven builds' ABI, which no user
// calls), and a go statement marked //qenv:go starts a simulation task:
//
//	//qenv:go
//	go func() { /* ... */ }()
//
// Inside a simulation a woven lock or wait group is the simulation's,
// keyed by the real object: taking it is a scheduling point and blocking
// on it is a simulated wait. Outside one the real method runs. A woven
// program therefore behaves as before outside a simulation, at the cost of
// a goroutine lookup per call: weave test builds, not production ones.
//
// Inside a simulation, every user of a woven object must be one of its
// tasks: a lock taken in a task and released by the goroutine that created
// the simulation would be released on the real mutex, which was never
// taken.
package weave

// The aspects' advice imports package woven: a tool file that imports
// this package makes it a dependency of the build.
import _ "github.com/chucklehead-dev/gotogether/woven"
