//go:build !windows

package internal

import (
	"os"
	"syscall"
)

func tryAcquireLock(file *os.File) bool {
	err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	return err == nil
}
