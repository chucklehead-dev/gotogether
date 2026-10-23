# Emitting steps: keys, outcomes, sinks

Keys in `Record` and annotations:
- `actor`, `process`, `thread`, `outcome` override the step's identity;
- `when:ok|error`: record only on success or only on failure;
- `obs.<name>`: an observation of the state after the step;
- `order.<domain>`: a logical clock;
- `invoked`: a `time.Time`, when the operation began, for a step recorded
  where it returned rather than where it took effect (below);
- anything else is an argument.

The final `error` is the outcome. An error can classify itself with a
`QuintOutcome() string` method (e.g. `"ambiguous"`).

**Record at the moment the step takes effect**, while holding the lock that
orders it, and before spawning work that depends on it.

Where you cannot (a client of a service you do not own, a history like
Jepsen's), record each operation when it returns with `"invoked", start`
and check with `qtest.Linearize()`: concurrent operations return in
another order than they took effect, and the recording order rejects
correct code (examples/kvstore's client history, every load tried). The
search tries the orders the operations' intervals allow (an operation
that returned before another began precedes it) and reports the one that
conformed, or the one that got furthest: a stale read shows as a
`Mismatch` no order explains. Each order tried is a quint replay from
the start, so keep such histories to tens of steps. Converted spans
carry their start as the invocation.

Sinks (`qobs.NewRecorder(sink)`, installed in a program's main with
`qobs.SetGlobal`; a test gets one per run from qtest, and `qtest.Global()`
installs it globally for goroutines the run starts):
- `MemorySink` for tests;
- `FileSink("steps.jsonl")` for a native step log;
- `NewOTelSink(tp)` for span events. Use a TracerProvider that never
  samples them out: a dropped step is a reported gap;
- `Tee{...}` to send steps to several sinks.

