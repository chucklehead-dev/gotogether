# Effects: what a step sends

A step can update its state correctly and still send the wrong request,
send it twice, or send nothing. Read this when a model has to say what a
service sends its dependencies (a capture, a publish, a notice), or when a
mutant that sends something wrong survives.

## Today's pattern: effects as steps at their site

The effect is a model action of its own, recorded where it lands (the
provider's commit, the consumer's read), and related to its cause by a
ghost and a history property (DESIGN §10.4: steps are recorded where
effects happen). payd's `captured`/`capturedCharged` and wallet's
`posted`/`allPosted` are this pattern:

```quint
var captured: str -> int // ghost: captures landed, by idempotency key
action capture(key: str, amount: int): bool = all {
  charged.keys().contains(key), amount == charged.get(key), // the payload, whole
  captured' = captured.put(key, getOr(captured, key) + 1),
  // ...
}
val capturedOnce = captured.keys().forall(k => captured.get(k) <= 1)
```

Done completely it is sound and needs no machinery. Its gaps:

- **Partial payloads.** The site records fewer fields than the effect has
  (wallet's first move only, checkout's `charge` with no amount): what is
  not recorded is not judged. Record every field the model can compute.
- **No cause.** A site step does not say which step caused it, so the
  model must allow it at any time (checkout's "the provider may charge
  any placed order at any time"), and cannot tell a duplicate the code
  made from one the network made.
- **Unrecorded effects.** An effect with no later reader (a notice, a
  metric) is recorded by nothing.
- **It waits.** "What this step must send, now" is judged at a settle, if
  one comes.

## A step's effects, compared at its step (I/O specifications)

When the effect's payload follows from the step (the amount from the
order), state it per action: a def over the post-state, over a sum type
the model declares, and let the binding compare it with what the step
made (`qobs.Effect`, qenv's doubles at the send; DESIGN §111).

```quint
type Effect = Capture({ order: str, amount: int })
def payEffects(order: str): List[Effect] = [Capture({ order: order, amount: orders.get(order) * PRICE })]
```

- The def reads the state after the step, as an expect does; a choice
  made inside the action must be a parameter.
- State boundary effects (a capture, a publish). An internal effect (an
  outbox row) specifies the design, not the requirement: name one only
  on purpose.
- The code's retries are one effect only if they are the same request:
  match `retried` with the idempotency key as a `same:` field, so a fresh
  key per try is two effects where the model makes one.
- Keep today's site steps for effects that land later (the provider's
  `charge`): they are another process's steps, related by a key.

checkout's `captureUnits` (the capture's units for its money) conformed in
every run under the site-step pattern alone, its `charge` carrying no
amount; with `payEffects` it is `Mismatch("pay")` in every run that pays.
