module example.com/roundtripfixture

go 1.27.3

require github.com/chucklehead-dev/gotogether v0.0.0

require (
	golang.org/x/mod v0.42.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/chucklehead-dev/gotogether => ../../..
