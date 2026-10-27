module github.com/chucklehead-dev/gotogether/examples/raftkv

go 1.26.1

require (
	github.com/chucklehead-dev/gotogether v0.0.0
	go.etcd.io/bbolt v1.5.0
	go.etcd.io/raft/v3 v3.7.0
	google.golang.org/protobuf v1.36.11
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/go-logr/logr v1.4.4 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/otel v1.46.0 // indirect
	go.opentelemetry.io/otel/metric v1.46.0 // indirect
	go.opentelemetry.io/otel/sdk v1.46.0 // indirect
	go.opentelemetry.io/otel/trace v1.46.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/chucklehead-dev/gotogether => ../..

replace github.com/chucklehead-dev/gotogether/qhegel => ../../qhegel
