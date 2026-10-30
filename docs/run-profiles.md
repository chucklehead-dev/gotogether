# Effective run profiles

Driven explorations retain the effective configuration used to make their
runs. Defaults and supported environment overrides are resolved before the
work starts; the profile describes that resolved plan. It does not reconstruct
configuration afterward from a seed or a verdict.

Evidence JSON carries `profiles`, identified by exploration `id`. A sweep or
mutation report retains its children's profiles: different rows or mutants
must not acquire one misleading aggregate configuration. Run-navigation JSON
carries the selected run's `profile`. The TUI shows the same profile text in
its scrollable run details, after the whole-run judgment. Moving the cursor
does not change the configuration that produced the run.

Navigation uses its own effective policy: the selected choice prefix is fixed,
then execution continues nominally. Its profile therefore identifies
navigation, one worker and the node being replayed, without claiming that the
originating seed's random choices or fault rates drove the navigated run.

## Reading a profile

- Mode and chooser identify how runs are selected. A supplied custom chooser
  is opaque; its fault policy is not inferred from the System's rates.
- Seed count and offset describe the initial sample. A short-mode top-up
  ceiling is the largest permitted sample when configured faults need more
  runs. Steering is additional work. A selected replay identifies its origin
  rather than claiming the ordinary seed sample ran.
- Parallelism is the effective worker count, including its default.
- Seeded fault rates describe the configured probability of nonnominal
  choices, including per-kind overrides. Exhaustive scheduling instead
  describes its delay/fault bounds, disabled kinds, maximum runs and partial
  policy; it does not draw at the System's seeded rates.
- Validation identifies the primary model binding, Quint command, backend selection policy and
  evaluator, including the primary checker's evaluator-confirmation sampling
  rate and seed. These settings describe the primary checker; separately
  registered composition checkers retain their own policies. Automatic
  backend selection can use TypeScript for batches and
  Rust for failure details; it is a policy, not a claim that every check used
  one backend.
  A configured confirmation rate can be raised for previously unconfirmed
  actions; observed judge counts show the resulting actual confirmations.
  An absent primary binding is shown explicitly rather than implying a
  primary model check occurred. A named mutant is also retained in its profile.
- Binding-derived model constants and selected invariants are retained, including
  workflow overrides, so individual sweep rows remain distinguishable.
- Simulation self-checks and the all-fault-family mode are explicit defaults,
  including when disabled. A scenario can override its simulation's self-checks
  and all-fault defaults after construction. History-search settings include ordering and pending-effect
  budgets, partition key and clock skew when applicable.

Configuration and outcomes remain distinct. Evidence `runs` counts completed
runs; search extent records whether enumeration finished or was truncated;
judge counts record who actually judged passing runs. Check receipts retain
their actual budgets, conclusions and unjudged obligations. A configured
limit does not prove that a run reached it.
Evidence also retains unjudged runs and run errors, including execution failures
that prevent model judgment.

A fault-filter wrapper records the kinds it requests to remain nominal;
explicit scripted faults from a custom chooser can bypass that wrapper. An
explicit schedule replay forces one schedule; it does not repeat bounded
enumeration or inherit that enumeration's fault exclusions. Fuzzing is driven
by its input bytes and does not claim a seeded sample.

The profile describes configuration owned by the driver. A separate
`execution` on a navigation node, and per-run `executions` in evidence, retain
observed `qenv.Sim.Run` invocations. Each invocation records the requested
virtual-time horizon and runtime. Deterministic runtimes also record the
effective scheduler step cap, wall-clock watchdog, cumulative steps before and
after the call, and whether the step cap or watchdog was reached. Native runtimes omit
scheduler limits they do not enforce. Repeated calls and multiple simulations
retain separate records in JSON; text groups identical policies to stay
readable. These whole-run observations also appear in the TUI independently
of its cursor.

A requested horizon is not elapsed time or proof that the system settled.
Arbitrary workload settings inside a callback cannot be inferred and are not
invented by the profile. Nor does a profile serialize executable custom chooser
code or make a composition receipt an executable replay plan.

The shared data and text live in `validate/runprofile`, independent of the
simulation engine and judgment machinery. The authoring configuration remains
the System and workflow configuration; callers do not need to maintain a
second profile by hand.
