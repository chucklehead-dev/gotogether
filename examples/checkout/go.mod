module github.com/chucklehead-dev/gotogether/examples/checkout

go 1.26.1

require github.com/chucklehead-dev/gotogether v0.0.0

replace github.com/chucklehead-dev/gotogether => ../..

require (
	github.com/chucklehead-dev/gotogether/examples/retrystorm v0.0.0
	github.com/chucklehead-dev/gotogether/qhegel v0.0.0-00010101000000-000000000000
	hegel.dev/go/hegel v0.9.27
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/ebitengine/purego v0.12.0-alpha.1.0.20261015130536-d456b1bb95e8 // indirect
	github.com/kr/pretty v0.3.1 // indirect
	github.com/rogpeppe/go-internal v1.14.1 // indirect
	go.opentelemetry.io/otel v1.46.0 // indirect
	go.opentelemetry.io/otel/trace v1.46.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	gopkg.in/check.v1 v1.0.0-20201130134442-10cb98267c6c // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/chucklehead-dev/gotogether/examples/retrystorm => ../retrystorm

replace github.com/chucklehead-dev/gotogether/qhegel => ../../qhegel
