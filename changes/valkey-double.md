- `qenv/simvalkey` is a separate module implementing the valkey-go client seam
  for a qenv simulation: command builders and Lua scripts, atomic script
  isolation, a server clock (`TIME`) and expiry, lost replies and hung calls,
  and an explicit asynchronous replica/failover profile. `WithTimeout` and
  `Client.Timeout` use virtual time; cancellation is distinct from an outage.
  Unsupported operations refuse the simulation rather than mimic a server
  error. This is a scoped client/command double, not a socket or cluster test.
