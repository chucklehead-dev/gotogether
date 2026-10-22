package bank

import "github.com/chucklehead-dev/gotogether/itf"

func readTrace(path string) (*itf.Trace, error) { return itf.ReadFile(path) }
