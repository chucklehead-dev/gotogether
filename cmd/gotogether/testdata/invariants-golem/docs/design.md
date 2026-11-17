# Two models, one page

The tree under this fixture carries the annotations of two models, each
qualified by its module. Each model's table is written by its own run,
from the fixture's directory:

    gotogether invariants -spec model/attempts.qnt -invariant safe -write docs/design.md ./...
    gotogether invariants -spec model/publication.qnt -exclude witness -links name -write docs/design.md ./...

## Attempts

<!-- gotogether:invariants attempts -->
| property | meaning | enforced by | checked by |
|---|---|---|---|
| `noLostAttempt` | No attempt is lost, whatever restores it. | `state.Restore` [../state/state.go:8](../state/state.go#L8) | `state.TestRestore` [../state/state_test.go:6](../state/state_test.go#L6) |
| `boundedAttempts` | At most five attempts. | `telemetry.Call.AttemptID` [../telemetry/call.go:13](../telemetry/call.go#L13) | `state.TestRestore` [../state/state_test.go:6](../state/state_test.go#L6) |
<!-- /gotogether:invariants attempts -->

<!-- gotogether:actions attempts -->
| action | recorded by | driven by |
|---|---|---|
| `attempt` | `telemetry.Call.AttemptID` [../telemetry/call.go:13](../telemetry/call.go#L13) | `state.TestAttemptsModel` [../state/attempts_mbt_test.go:13](../state/attempts_mbt_test.go#L13) |
| `idle` | — | skipped in `state.TestAttemptsModel` [../state/attempts_mbt_test.go:15](../state/attempts_mbt_test.go#L15) |
<!-- /gotogether:actions attempts -->

## Publication

<!-- gotogether:invariants publication -->
| property | meaning | enforced by | checked by |
|---|---|---|---|
| `noExpiredServe` | A page is never served expired: its lease is checked first. | `Serve` [state.go](../state/state.go) | `TestServe` [state_test.go](../state/state_test.go) |
<!-- /gotogether:invariants publication -->
