module github.com/chucklehead-dev/gotogether/examples/jobqueue

go 1.26.1

require (
	github.com/chucklehead-dev/gotogether v0.0.0
	github.com/chucklehead-dev/gotogether/qhegel v0.0.0-00010101000000-000000000000
	hegel.dev/go/hegel v0.9.27
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/ebitengine/purego v0.12.0-alpha.1.0.20261015130536-d456b1bb95e8 // indirect
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
