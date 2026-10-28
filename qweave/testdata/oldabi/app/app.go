// Package app is code to cover.
package app

import _ "github.com/chucklehead-dev/gotogether/woven"

// Double doubles n.
func Double(n int) int {
	if n > 0 {
		return 2 * n
	}
	return 0
}
