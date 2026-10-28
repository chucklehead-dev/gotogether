# Services: HTTP, gRPC, streams

## HTTP servers (examples/kvstore)

kvstore is an HTTP service with CAS (`kvserver`, `kvload`): steps are
recorded in the handler where the write takes effect, or converted from
the server's OTel spans (`convert` rules), and validated with the
binding. Run it for real and validate its step log (`run-demo.sh`).

## net/http in a simulation: `qenv/qhttp` (examples/payd)

Read this before simulating a service whose API is HTTP, or a client
that calls one (a payments processor, a webhook): the handler and the
client stay as they are. [examples/payd](../../../examples/payd) is a
service and its SDK done so: steps recorded by a RoundTripper around the
SDK's transport and in a processor double, none in the service.

```go
var srv *qhttp.Server
p := sim.Process("api", true, func(ctx context.Context) {
	srv.Handle(ctx, api.NewHandler(store)) // at every incarnation: refused before it
})
srv = qhttp.NewServer(sim, "api", p)          // a qenv.Service carries the bytes
tr := qhttp.NewTransport(srv)                 // an http.RoundTripper, routed by host
tr.Timeout, tr.Refuse = 2*time.Second, true   // virtual time; "http.api.conn" refused
client := &http.Client{Transport: tr}         // the SDK's client, unchanged
sim.Spawn("sdk", func(ctx context.Context) { /* client.Post(srv.URL()+"/payments", ...) */ })
```

- Requests and responses are wire bytes (`Request.Write`,
  `http.ReadRequest`; the response as net/http's server writes it: Date
  from the server's clock, a sniffed Content-Type, Content-Length or
  chunked), so nothing is shared between client and handler.
- Outcomes are what a real client sees: a refused connection is a dial
  `*net.OpError` (`errors.Is(err, syscall.ECONNREFUSED)`: nothing sent, a
  retry is safe); a request that reached a restarted server before its
  `Handle` a read one with `ECONNRESET` (outcome unknown: a duplicate may
  have taken effect); a missing response a `*qhttp.TimeoutError` (the
  `*url.Error`'s `Timeout()` is true, `errors.Is(err,
  context.DeadlineExceeded)`; outcome unknown); a full queue
  (`srv.Service.Capacity`) a 503. The `rpc.<name>.*` faults, late
  requests and the crash window (`srv.Service.NoCrashWindow`) are the
  Service's.
- Time: the Transport's `Timeout` and a request context's deadline are
  virtual. A deadline is converted (`Sim.ToVirtual`) the first time the
  simulation sees it, its remaining time rounded up to a grid of round
  timeouts (100 ms, 150 ms, 200 ms, 250 ms, 300 ms, 400 ms, 500 ms, 600 ms,
  800 ms, 1 s, ...: 700 ms becomes 800 ms), which takes back the test
  process's own jitter, then used up in virtual time: a retry under it
  after it passed fails at once, unsent. `qhttp.WithTimeout(ctx, d)`, where
  the code can call it, gives the exact timeout, counted from the call in
  virtual time. A deadline the wall clock passed while no virtual time
  did fails nothing. `http.Client.Timeout` reaches the Transport as such a
  deadline. The context's own timer is wall time: `ctx.Err()` approximately
  never fires in a fast run, so a retry loop must stop on the request's
  error (`context.DeadlineExceeded`) or an attempt count, not on
  `ctx.Err()`.
- A handler runs as a task of the server's process, with the task's
  context as `r.Context()`: record steps and use the Store in it. Its
  panic fails the run (net/http would recover it). A handler that starts
  goroutines (`http.TimeoutHandler` among them) needs weaving or gosim.
- Not simulated: TLS (https reaches the same server), HTTP/2, trailers,
  100-continue, Hijack, connection reuse.

## gRPC services: `qenv/simgrpc` (a module; examples/grpcledger)

```go
srv := simgrpc.NewServer(sim, "ledger", proc)            // a grpc.ServiceRegistrar in the simulation
ledgerpb.RegisterLedgerServer(srv, &ledger.Server{DB: store})
client := ledgerpb.NewLedgerClient(srv.Dial())           // a grpc.ClientConnInterface across qenv.Service
```

- The generated code is unchanged. Faults are `grpc.<name>.conn`
  (Unavailable before sending), `rpc.<name>.request`
  (drop/duplicate), `rpc.<name>.delay` (late: just after the caller's
  timeout; veryLate: a timeout after it, after a retry that backed off;
  lateDuplicate: a copy that late; slow: late but in time),
  `rpc.<name>.response` (lost), and crashes of
  `proc`. A missing response is `DeadlineExceeded` after `Conn.Timeout`
  of virtual time.
- Put the retry policy in client code (both tiers run it), not in a gRPC
  service config.
- Record steps in the handler at the commit (`Txn.OnCommit`). For the real
  tier, also set them as span attributes and convert the otelgrpc spans
  (`ledger.convert.yaml`, a commit-order attribute as `order:`).
- Lock rows in one order. `qenv.Store` detects a cycle of lock waits and
  aborts the transaction closing it (`qenv.ErrDeadlock`, an ErrConflict
  that `qenv.Retry` retries), as a real database does.
- A crash can fall between a handler's commit and its response
  (`qenv.Service`'s crash window, closed by `NoCrashWindow`): a server
  that dies after committing, whose client retries.

## Streams: Kafka and Kinesis (examples/streams)

- **Streams (Kafka, Kinesis).** Seams: `qenv/simlog`'s `Writer` (Produce
  with a done callback, Flush) and `Reader` (Poll, Commit), records with
  byte values. `qenv/simlog` is the simulated log (`simlog.New`):
  - named faults `log.<name>.produce` (lost, lostAck), `log.<name>.commit`
    (lost, lostAck) and `log.<name>.fetch` (rewind);
  - evictions as `rebalance.<name>.<consumer>` nemeses, with fencing;
  - an idempotent producer client (`Log.Producer`), group members
    (`Log.Member`);
  - a commit reported where it lands (`Log.OnCommit`): record it there;
  - its contract checked under `GOTOGETHER_CHECK=1`.

  The same seams over real clients are modules: `qenv/simlog/kafkalog`
  (franz-go) and `qenv/simlog/kinesislog` (the AWS SDK, with an
  in-process endpoint, `kinesisfake`); each reports a commit to
  `Reader.OnCommit` when the client is told it landed.

  Record `read` at the consumer and let the model learn the log from it:
  the same binding then checks kfake (franz-go's in-process Kafka) and a
  fake Kinesis endpoint. A publisher should drain an outbox, so a restart
  resumes instead of redoing everything.
