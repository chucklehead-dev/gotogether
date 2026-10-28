// Package woven stands for a core of another woven ABI than qweave's:
// checkABI must refuse to weave against it.
package woven

// ABI0 is the ABI of this core.
const ABI0 = 0

// Hit is a block's call, as an old ABI may have had it.
func Hit(id int) {}
