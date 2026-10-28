# ci/consumer: the toolkit built from outside

A user's module, not the toolkit's: an order-sync service whose orders
arrive over gRPC (served by `qenv/simgrpc` in a simulation) and go out on
a log (`qenv/simlog`, Kafka through `qenv/simlog/kafkalog` in
production), explored with `qtest.System` and shrunk with `qhegel`.

Its go.mod requires nothing: CI's `consumer` job copies it outside the
repository and `go get`s the published modules at the pushed commit, with
no replace and no workspace, so nothing builds only because the checkout
is beside it (ROADMAP I14). Two builds: the satellites at the commit
with the core at the newest of their pins (what `go get` of them gives
a user), then the core at the commit too.

```sh
cp -r ci/consumer /tmp/consumer && cd /tmp/consumer
GOWORK=off go get github.com/chucklehead-dev/gotogether/qenv/simgrpc@main \
  github.com/chucklehead-dev/gotogether/qenv/simlog/kafkalog@main \
  github.com/chucklehead-dev/gotogether/qhegel@main
GOWORK=off go mod tidy && GOWORK=off go test ./...
```
