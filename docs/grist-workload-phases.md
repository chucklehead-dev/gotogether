# Required workload phases

A workload can finish its setup calls without establishing what its later
operations need. A command may refuse, its answer may remain unknown, or an
asynchronous processor may still be doing the work. Named gristsim phases make
those prerequisites part of the workload definition.

`Config.Phases` runs before the configured random clients. Each phase names
its required calls and optional observations. The next phase, and then the
random workload, starts only after those requirements have been satisfied.

```go
cfg := gristsim.Config{
    Phases: []gristsim.Phase{{
        Name: "prepare order",
        Timeout: 10 * time.Second,
        Faults: gristsim.FaultsDisabled,
        Calls: []gristsim.Call{{Name: "place", Args: Order{Order: "a"}}},
    }},
    Send: []string{"cancel", "confirm"},
}
```

Calls require an answered `ok` outcome by default. A deliberate refusal must
name its expected outcome with `Expect`, such as `Expect: "invalid"`. A lost
answer or a different refusal does not count as successful preparation. An
empty call tenant expands to each configured tenant; setting `Tenant` selects
one. Arguments use the block's normal argument type or JSON.

`Call.Caller` selects the application principal. Its default is the stable
`phase` caller across stages; renaming or splitting a phase does not change
authorization identity. Set it explicitly when the scenario needs different
principals. Phase names remain execution labels.

## Observe asynchronous readiness

An accepted command is enough for a directly committed prerequisite. For an
asynchronous consequence, add `Await` observations through real query APIs.
`Observation.Call` selects the query; its optional `App` identifies an actual
peer from `Config.Peers`, with nil selecting the workload's main App. `Result`
is the complete expected JSON result. Object field order does not matter;
arrays and field values must match. Use explicit JSON `null` when that is the
expected result, rather than leaving `Result` unspecified.

The inventory example places an order at checkout, then asks inventory's
`allocation` query until it observes `{ "units": 1, "status": "held" }`.
That query reads inventory's own current allocation through its API. Its
independent local model checks the same query answer; the barrier adds no
model predicate and does not fabricate a native commit.

```go
phase := gristsim.Phase{
    Name: "reserve inventory",
    Timeout: 10 * time.Second,
    Faults: gristsim.FaultsDisabled,
    Calls: []gristsim.Call{{Name: "place", Args: Order{Order: "a"}}},
    Await: []gristsim.Observation{{
        Call: gristsim.Call{App: inventory.App, Name: "allocation", Args: Order{Order: "a"}},
        Result: inventory.Allocation{Units: 1, Status: "held"},
    }},
}
```

The finite timeout bounds the whole phase in true virtual time, including
in-flight requests and retries. Each query poll uses the normal recorded
client path. Polling is an observable query and a bounded delay between
observations, rather than a sleep that assumes the work has completed.

The condition is an observation at the boundary. Competing commands can change
the state afterward; the phase does not make a promise about every later state.

## Keep fault boundaries explicit

Every phase declares its fault policy:

| Policy | Meaning |
|---|---|
| `FaultsConfigured` | Continue using the run's configured fault choices, including scripts. |
| `FaultsDisabled` | Suppress new fault choices while meeting these prerequisites; scheduling still runs normally. |

Disabling new faults does not heal a partition that already exists. No phase
calls `Settle`, changes scheduling to a recovery schedule, or records a settle
marker. The random clients resume the configured fault policy after the named
phases. The normal final settle still belongs to the end of the full workload.

Choose the policy based on the scenario. A test that prepares a known starting
state can disable setup faults and exercise faults afterward. A test of setup
under failures keeps configured faults and accepts that its prerequisite may
not be reached. The delayed-reservation contrast in the inventory example
does the latter: cancellation can overtake reserve, without a readiness check
silently healing away the delay.

## Read failures as workload evidence

A failed required outcome or an expired phase stops subsequent work. It does
not establish a model counterexample or prove that an eventual obligation is
impossible. Check the configured prerequisite and the actual observed answer.

Run execution data records each phase's name, policy, timeout, virtual timing,
state and requirements. Requirements retain the target App/tenant/action,
expected and observed outcome or query result, and logical call/poll count.
These counts do not claim to count every transport retry inside a call.
An in-flight driver request is distinct from the last answered request, so a
timeout can retain the last actual query result while identifying the unanswered
request. Neither a pending retry nor its absence is a fabricated query result.

The same records appear in evidence JSON and navigation's execution data,
including when preparation fails before model judgment. The TUI shows the
workload outcome alongside its execution details, independently of the model
cursor. A completed phase reports the prerequisites reached by this workload;
normal conformance and protocol assertions still judge the captured history.

Typed prerequisite failures (`PhaseError`) refuse mutation assessment and
model-failure shrinking; they cannot count as mutant kills. Ordinary application
errors and panics retain their existing mutation behavior. Direct `gristsim.Run`
exposes the typed error for tests of unsuccessful preparation.

Use phases to establish starting conditions. Keep the behavior being investigated
in the subsequent workload and its independent model checks. If a mutant prevents
a prerequisite, the scenario has not reached the intended test; adjust the workload
to expose its counterexample. The inventory missing-release mutant keeps the
reservation prerequisite, then lets the protocol model judge cancellation.
