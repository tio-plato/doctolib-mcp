//go:build !windows

package gate

import (
	"os"
	"syscall"
)

// lockExclusive waits until it holds an exclusive lock on f.
func lockExclusive(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
}

func unlockExclusive(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
