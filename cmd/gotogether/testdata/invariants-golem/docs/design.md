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
| `boundedAttempts` | At most five attempts. | `telemetry.Call.AttemptID` [../telemetry/call.go:10](../telemetry/call.go#L10) | `state.TestRestore` [../state/state_test.go:6](../state/state_test.go#L6) |
<!-- /gotogether:invariants attempts -->

## Publication

<!-- gotogether:invariants publication -->
| property | meaning | enforced by | checked by |
|---|---|---|---|
| `noExpiredServe` | A page is never served expired: its lease is checked first. | `state.Serve` [../state/state.go](../state/state.go) | `state.TestServe` [../state/state_test.go](../state/state_test.go) |
<!-- /gotogether:invariants publication -->
