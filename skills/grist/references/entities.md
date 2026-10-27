# Commands that touch several entities

A command that reads or changes more than one entity (a transfer moves
two accounts; an order reserves several items) is written so its read
set, and so its condition, is exactly what its rule depends on.
examples/wallet-grist's `transfer` is the walk-through.

- **One event per entity touched.** A transfer appends two `moved`
  events, `{account, amount}` (signed), one per account, in one batch,
  not one `transferred{src, dest, amount}`. Each event carries the tag
  of the entity it changes.
- **One keyed read model per entity kind**, keyed by that tag:
  `grist.NewKeyedReadModel(App, "balances", 1, grist.Keys("account"),
  ...)` folds `moved` per account. `NewKeyedReadModel` takes one key,
  which must be a tag of every handler's type: that is why the event is
  per entity.
- **The rule reads each entity it depends on** through the keyed read
  model (`Balances.For(r, account)`): each read is one selector, so the
  append is conditioned on those entities' events only. Two transfers
  from different accounts commit side by side; a transfer from a shared
  credit line reads, and is conditioned on, every account of the line,
  so a concurrent transfer from either conflicts (no write skew).
- **The model's `needs`** for a command is that set of reads
  (`covers(reads, needs(src))`), so a runtime that dropped one is caught
  at every commit.
- An entity read only to validate the request (that a destination
  exists, where existence never changes) need not be read through the
  store at all: a rule over the arguments reads nothing.
