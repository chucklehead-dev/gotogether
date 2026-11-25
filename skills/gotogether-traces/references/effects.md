# Effects: comparing what a step sent with the model's

Read this when a binding should check what a step sends (a request, a
publish), not only the state it leaves: a capture of the wrong amount, a
retry under a fresh idempotency key, a notice never sent. The model side
(an effects def per action) is the gotogether-patterns skill's
references/effects.md; the design is DESIGN §112.

## Record

- Inside a simulation, qenv's doubles record every send: `Service.Call`
  as `rpc.<name>.request` (the request's exported fields, by Go name),
  `Queue.Publish` as `queue.<name>.publish`, simlog's `Produce` as
  `log.<name>.produce`, qhttp's Transport as `http.<server>.request`
  (method, path, body, `header.<Name>`).
- Elsewhere, record your own: `qobs.Effect(ctx, "mail.send", "to", addr)`.
- Attribute them to their step. Record the step as a call and send
  through its scope, so every try is the call's:

  ```go
  call := qobs.Invoke(ctx, "pay", "order", id)
  pctx := call.Scope(ctx)
  err := capture(pctx, key, id, amount) // its requests are pay's effects
  call.Done(err)
  ```

  Without a scope an effect is its task's next step's (a step recorded
  at the commit the task makes). An effect made after `Done` under the
  scope is late.
- Outside a simulation a step says its effects were observed only under
  `qobs.NewRecorder(sink, qobs.ObserveEffects())`.

## Bind

```yaml
effects:
  kinds:
    Capture:
      from: rpc.provider.request
      where: { Op: capture }
      fields: { order: '{{.Order}}', amount: '{{.Amount}}' }
      same: [Key]
actions:
  pay:
    match: retried    # exact | retried | set
```

The action's def is `<action>Effects` unless `effects:` names another
(`"-"`: none). `retried` collapses consecutive tries of one effect whose
`same:` fields agree: a fresh key per try stays two effects.

## Read the verdict

| Report says | Means | Next |
|---|---|---|
| `the effects made are not the model's payEffects(...)`, `first difference: effect 1, field amount` | a payload differs | the code's request, or the def |
| `first difference: effect 2: nothing, expected ...` | an effect is missing | a dropped send |
| `... expected nothing` | an extra or repeated effect | a duplicate send; `match` |
| `was tried again under another Key` | a retry with a fresh key | one key per request |
| `was made after the step completed` | a goroutine still sending | an owed effect, judged at its site |
| `the step recorded no effect scope` (exit 2) | nothing recorded effects for it | a scope, a simulation, `ObserveEffects`, or `effects: "-"` |

`qtest.MutateSteps` mutates effect records too (`effect-drop`,
`effect-dup`, `effect-swap`, `effect-corrupt`): a survivor of a kind the
binding maps is a field its fields do not compare.
