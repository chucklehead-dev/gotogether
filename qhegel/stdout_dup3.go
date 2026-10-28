//go:build linux && (arm64 || loong64 || riscv64)

package qhegel

import "syscall"

// dup2 is dup3 where Linux has no dup2 system call.
func dup2(from, to int) error { return syscall.Dup3(from, to, 0) }
