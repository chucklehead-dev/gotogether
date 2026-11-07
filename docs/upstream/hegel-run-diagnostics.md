# Hegel test diagnostics and Run corpus identity

Observed with `hegel.dev/go/hegel v0.9.30` on Go 1.27.3, Linux/amd64.
This note records two public-API limitations; no upstream issue has been sent.

## Run-level diagnostic loss

A real FilterTooMuch health-check failure marks the test failed without printing
its native error:

```go
func TestHealthCheck(t *testing.T) {
    hegel.Test(t, func(tc *hegel.T) {
        _ = hegel.Draw(tc, hegel.Booleans())
        tc.Assume(false)
    }, hegel.WithTestCases(60), hegel.WithDatabase(""))
}
```

`go test -v -run '^TestHealthCheck$'` prints the test name and FAIL, with no
health-check reason. The same body through `hegel.Run` returns:

```text
property test failed: FailedHealthCheck: FilterTooMuch ...
50 inputs were rejected ... only 0 valid inputs were generated
```

In `runner.go`, `runWithContext` returns RUN_STATUS_ERROR's diagnostic wrapped
in `errPropTestFailed`. `Test` handles that marker with `t.Fail()` and drops the
returned message. Logging the returned error before marking failure would fix
this independently of any new API. A regression should force a real native
health-check error and require both failed status and its diagnostic.

## Run does not persist examples without a database key

Using a fresh, existing directory, this failing property leaves it empty:

```go
dir := t.TempDir()
err := hegel.Run(func(tc hegel.TestCase) {
    _ = hegel.Draw(tc, hegel.Integers(0, 1000000))
    tc.Errorf("corpus sentinel")
}, hegel.WithDatabase(dir))
entries, _ := os.ReadDir(dir) // empty, despite the nonnil property failure
```

The same failure through `hegel.Test(t, ...)`, with `WithDatabase(dir)`, writes
examples. Both seeded and unseeded Run calls were checked; creating the directory
first does not change the result.

`runner.go` has a private `withDatabaseKey` option. `Test` supplies `t.Name()`;
`Run` has no public equivalent. Its automatic TestLocation is for Antithesis
assertion reporting, not database identity. Separate physical directories alone
therefore do not preserve Test's persistence when switching to Run.

The smallest API addition for callers that need Run's returned error and native
persistence is to expose the existing option:

```go
// WithDatabaseKey identifies this property's examples within its database.
// Distinct properties sharing a database must use distinct keys.
func WithDatabaseKey(key string) Option {
    return func(o *runOptions) {
        o.addSetting(func(ctx *libhegel.Context, s *libhegel.Settings) error {
            return s.DatabaseKey(ctx, key)
        })
    }
}
```

Test can use the same exported option. Admission should save a failing example
under key A, show that PhaseReuse under B runs no saved failure, then save B and
reproduce each independently. Disabling the database must still suppress writes.

## Current gotogether boundary

`qhegel.Test` exposes `hegel.TestCase` callbacks but invokes the supported SDK
Test entry point privately. This retains native test identity, corpus reuse,
output attribution and short-mode handling. Its health-check diagnostic
limitation remains explicit. It does not use a private symbol, unsafe access,
file-descriptor capture, a replacement SDK, or a public compatibility adapter.
The native corpus tests exercise default and shared custom databases through
actual save/replay behavior.
