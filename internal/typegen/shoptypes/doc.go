// Package shoptypes is the Go types gotogether types generated from
// internal/typegen/testdata/shop.qnt, a model with every kind of type the
// generator maps: its round-trip test decodes every state of a quint run
// into State. cmd/gotogether's TestTypesCheck fails when shop_types.go is
// not what the model generates now.
package shoptypes

//go:generate go run ../../../cmd/gotogether types -spec ../testdata/shop.qnt -main shop -package shoptypes -o shop_types.go
