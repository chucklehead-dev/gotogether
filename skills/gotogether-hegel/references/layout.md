# Where Hegel tests live in your repository

Read this before adding Hegel tests to a project, or when Hegel machines sit
in a module of their own and production code exports methods only they call.

Hegel runs its engine in-process: a native library loaded through purego,
shipped per platform inside the hegel module. gotogether keeps that out of
its core (`qhegel` is a module of its own) so that a program that only
records steps never depends on it. That is a rule for a library everyone
imports, not a layout to copy: your own Hegel tests belong in the package
they test.

## The pattern

Put the package's Hegel machines in its own `_test.go` files, behind a build
tag:

```go
//go:build hegel

package limiter

// the machine sees the package's internals: no exported SetDown or Failover
```

- **The tests see internals.** Methods a test alone calls (golem's
  `SetDown`, `Failover`, `Buckets`, `ReplayOne`, `EndGrace`) stay unexported,
  and a test reaches unexported state directly. A Hegel module of its own
  can only use what the package exports.
- **A plain `go test ./...` is unaffected,** and so is a platform libhegel
  does not ship for: without `-tags hegel` the files are not compiled.
- **CI runs the tag:** `go test -tags hegel ./...` in the job that runs them.
  Tagged tests that no job runs rot without anyone seeing it.
- **gopls and `go vet` need the tag** to see the files: set `-tags hegel` in
  the editor's build flags, and vet with it in CI.

## What it costs

`go mod tidy` reads every build tag, so the module's `go.mod` lists hegel
(and purego) either way. A test-only requirement is never linked into the
binaries.

- **An application** (golem): it costs nothing. No one imports the module,
  and its binaries do not contain Hegel.
- **A library**: its users see the requirement in their `go mod graph`, and
  it takes part in their version selection, though their builds never
  compile it. If that matters to its users, keep the Hegel tests in a
  module of their own beside the library, and accept that they test only
  what the library exports.

The connect drivers are the same either way: a driver in the package's tests
sees what a Hegel machine there sees.
