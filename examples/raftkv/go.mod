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
	github.com/kr/pretty v0.3.1 // indirect
	github.com/rogpeppe/go-internal v1.14.1 // indirect
	go.opentelemetry.io/otel v1.46.0 // indirect
	go.opentelemetry.io/otel/trace v1.46.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	gopkg.in/check.v1 v1.0.0-20201130134442-10cb98267c6c // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/chucklehead-dev/gotogether => ../..

replace github.com/chucklehead-dev/gotogether/qhegel => ../../qhegel
