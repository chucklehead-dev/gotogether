//go:build unix && !(linux && (arm64 || loong64 || riscv64))

package qhegel

import "syscall"

func dup2(from, to int) error { return syscall.Dup2(from, to) }
