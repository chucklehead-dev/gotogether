// Package woven stands for a core of the woven ABI1 from before
// //quint:guards: it has no MutexUnlockGuarded, so qweave must refuse to
// weave a guarded field's Unlock against it.
package woven

import "sync"

// ABI1 is the ABI of this core.
const ABI1 = 1

// MutexUnlock is (*sync.Mutex).Unlock.
func MutexUnlock(m *sync.Mutex) { m.Unlock() }
