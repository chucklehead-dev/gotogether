//go:build linux || darwin || freebsd || openbsd || netbsd || dragonfly

package quintcmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

// All participating binaries use this fixed protocol and capacity. Files are
// never removed: replacing a locked inode would create a second independent pool.
func acquire(ctx context.Context) (func(), error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return nil, fmt.Errorf("quint admission: %w", err)
	}
	if err := os.MkdirAll(cache, 0700); err != nil {
		return nil, fmt.Errorf("quint admission: %w", err)
	}
	dir := filepath.Join(cache, "gotogether-quint-slots-v1")
	if err := os.Mkdir(dir, 0700); err != nil && !os.IsExist(err) {
		return nil, err
	}
	st, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	owner, ok := st.Sys().(*syscall.Stat_t)
	if !st.IsDir() || st.Mode().Perm() != 0700 || !ok || owner.Uid != uint32(os.Getuid()) {
		return nil, fmt.Errorf("quint admission: insecure directory %s", dir)
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for i := 0; i < 2; i++ {
			fd, err := syscall.Open(filepath.Join(dir, strconv.Itoa(i)), syscall.O_CREAT|syscall.O_RDWR|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0600)
			if err != nil {
				return nil, fmt.Errorf("quint admission: %w", err)
			}
			var stat syscall.Stat_t
			err = syscall.Fstat(fd, &stat)
			if err == nil && (stat.Mode&syscall.S_IFMT != syscall.S_IFREG || stat.Uid != uint32(os.Getuid()) || stat.Mode&0777 != 0600) {
				err = fmt.Errorf("insecure token file")
			}
			if err != nil {
				syscall.Close(fd)
				return nil, fmt.Errorf("quint admission: %w", err)
			}
			err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB)
			if err == nil {
				return func() { _ = syscall.Close(fd) }, nil
			}
			syscall.Close(fd)
			if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
				return nil, fmt.Errorf("quint admission: %w", err)
			}
		}
		timer := time.NewTimer(20 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
