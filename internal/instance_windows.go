//go:build windows

package internal

import (
	"os"

	"golang.org/x/sys/windows"
)

func tryAcquireLock(file *os.File) bool {
	var overlapped windows.Overlapped
	err := windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0,
		1,
		0,
		&overlapped,
	)
	return err == nil
}
