# Valkey in a simulation

`simvalkey` replaces a valkey-go client inside a qenv simulation. Application
command builders and Lua sources run through a RESP2 command/result profile at the client seam; no real socket
server or Valkey process is involved. This keeps the operations that can
interleave at scheduling seams while a script's commands execute atomically.
The module keeps the Valkey client and Lua dependencies outside the core.

## Use the production client seam

```go
package example

import (
    "context"
    "testing"
    "time"

    "github.com/chucklehead-dev/gotogether/qenv"
    "github.com/chucklehead-dev/gotogether/qenv/simvalkey"
    "github.com/valkey-io/valkey-go"
)

func TestScript(t *testing.T) {
    s := qenv.New(qenv.Nominal{})
    defer s.Close()
    server := simvalkey.New(s, "cache")
    client := server.Client()
    script := valkey.NewLuaScript(`return redis.call('INCR', KEYS[1])`)
    s.Spawn("counter", func(ctx context.Context) {
        ctx, cancel := simvalkey.WithTimeout(ctx, time.Second)
        defer cancel()
        n, err := script.Exec(ctx, client, []string{"calls"}, nil).AsInt64()
        if err != nil || n != 1 {
            t.Errorf("increment = %d, %v; want 1", n, err)
        }
    })
    if err := s.Run(time.Second); err != nil {
        t.Fatal(err)
    }
}
```

`Client.Timeout` is a virtual per-call timeout; zero leaves the call unbounded.
Use `WithTimeout` to give a context an exact simulation deadline, and schedule
ordinary cancellation from a simulated task so the run replays. Plain
real-clock context deadlines are refused before dispatch: use this virtual
helper or `Client.Timeout` for simulated call deadlines. Cancellation remains
distinct from timeout or an unavailable connection.

## Faults and replication profile

Fault names include the server's name; option zero is nominal:

| Kind | Options | Meaning |
| --- | --- | --- |
| `valkey.<name>.request` | `outage`, `hang` | Refuse before dispatch, or suppress the request's response. |
| `valkey.<name>.response` | `lost` | Apply the operation but lose its reply. |
| `valkey.<name>.replication` | `lag` | Leave the single replica's checkpoint behind the primary. |
| `valkey.<name>.failover` | `lose` | Promote that checkpoint, permitting loss of acknowledged writes. |

`Server.Replicate` and `Server.Failover` expose the corresponding incident
controls. Script cache state is volatile. Replication is one explicit
asynchronous checkpoint, not a cluster protocol. Use `WAIT` to distinguish
replica acknowledgement from merely receiving the primary's reply; a timeout
can return zero rather than pretending the write was replicated.

## Semantics and boundaries

Atomic script execution means isolation, not rollback. A script that writes
and then raises a runtime error can leave those writes applied, as Valkey does.
Unsupported operations are a simulation-profile refusal, not a server error
that application retry code may turn into a successful run.

The server's `TIME` and key expiry use simulation-controlled server time.
They must not inherit a gateway's skewed clock. Expiry is observed at its
absolute server deadline; a client canceling a request does not rewind that
clock or undo a command already applied.

A call that gives no response is distinct from an immediate unavailable
connection. Cancellation after dispatch can have an unknown outcome, so retry
safety belongs to the application. A simulated hang waits at a qenv seam;
`Sim.Hang` is still the wall-time watchdog for code blocked outside simulation,
such as a real mutex held across a client call.

The first command profile covers strings and counters (`GET`, `SET`, `INCR`,
`INCRBY`, `DEL`, `EXISTS`), hashes, sorted sets, `PEXPIRE`/`PTTL`, `TIME`,
`EVAL`/`EVALSHA`, and script cache operations. Command options outside that
profile are refused explicitly. The Lua environment is sandboxed; deterministic
resource limits reject unbounded scripts as a harness limitation rather than
pretending Valkey returned a server error.

This is a bounded command/client profile, not a complete Valkey server. It does
not test socket framing, connection pools, TLS, cluster routing, or replica
transport. The server's failover controls model explicitly chosen replication
loss rather than claiming a full replication protocol. Use a real-Valkey tier
for behavior outside the supported profile and differential checks.

## Validation

The regular suite explores concurrent admission against a Quint model, with a
split-read/write negative control, and checks virtual deadlines, caller crashes,
expiry, cache recovery, replica loss and pipeline interleavings under race checks.
The supported command/Lua profile also has a native differential tier against
Valkey 9.1.3 (the CI image is pinned by digest). With a disposable server:

```sh
GOTOGETHER_VALKEY_ADDR=127.0.0.1:6379 go test -race -tags valkeyreal -run '^TestRealContract$' -count=1 .
```

That tier compares supported results and server-error classes, not backend-specific
Lua stack text. Unordered hash fields are compared as maps; `TIME` clocks are
independent, so its response shape is compared and simulation-clock behavior is
checked separately. Replication transport and faults are outside that real-server
comparison; the simulated replica profile has its own contract tests.
