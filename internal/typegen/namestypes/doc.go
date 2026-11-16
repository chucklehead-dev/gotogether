// Package namestypes is the Go types gotogether types generated from
// internal/typegen/testdata/names.qnt, a model of names the generator
// renames and types it keeps as itf.Value; internal/typegen's TestNames
// fails when names_types.go is not what the model generates now.
package namestypes

//go:generate go run ../../../cmd/gotogether types -spec ../testdata/names.qnt -main names -package namestypes -o names_types.go
