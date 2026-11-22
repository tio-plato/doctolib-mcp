package gate

import (
	"os"

	"golang.org/x/sys/windows"
)

// allBytes, as both the low and the high half of the length, covers the whole file, as cmd/go's filelock does.
const allBytes = ^uint32(0)

// lockExclusive waits until it holds an exclusive lock on f: without LOCKFILE_FAIL_IMMEDIATELY, LockFileEx on
// the synchronous handle that [os.OpenFile] gives returns only once it has the lock.
func lockExclusive(f *os.File) error {
	return windows.LockFileEx(
		windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, allBytes, allBytes, new(windows.Overlapped),
	)
}

func unlockExclusive(f *os.File) error {
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, allBytes, allBytes, new(windows.Overlapped))
}
