//go:build windows
// +build windows

/* ipp-usb - HTTP reverse proxy, backed by IPP-over-USB connection to device
 *
 * See LICENSE for license terms and conditions
 *
 * File locking -- Windows version
 */

package main

import (
	"os"
	"syscall"
	"unsafe"
)

// FileLockCmd represents set of possible values for the
// FileLock argument
type FileLockCmd int

const (
	// FileLockWait command used to lock the file; wait if it is busy
	FileLockWait FileLockCmd = 0x02 // LOCKFILE_EXCLUSIVE_LOCK

	// FileLockNoWait command used to lock the file without wait.
	// If file is busy it fails with ErrLockIsBusy error
	FileLockNoWait FileLockCmd = 0x03 // LOCKFILE_EXCLUSIVE_LOCK | LOCKFILE_FAIL_IMMEDIATELY

	// FileLockUnlock command used to unlock the file
	FileLockUnlock FileLockCmd = 0
)

var (
	modkernel32      = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx   = modkernel32.NewProc("LockFileEx")
	procUnlockFileEx = modkernel32.NewProc("UnlockFileEx")
)

// FileLock manages file lock
func FileLock(file *os.File, cmd FileLockCmd) error {
	if cmd == FileLockUnlock {
		return FileUnlock(file)
	}

	h := syscall.Handle(file.Fd())
	ol := new(syscall.Overlapped)
	flags := uint32(cmd)

	r1, _, err := procLockFileEx.Call(
		uintptr(h),
		uintptr(flags),
		0,
		1, 0,
		uintptr(unsafe.Pointer(ol)),
	)

	if r1 == 0 {
		// ERROR_LOCK_VIOLATION indicates the lock is held by another process
		if err == syscall.Errno(33) {
			return ErrLockIsBusy
		}
		return err
	}
	return nil
}

// FileUnlock releases file lock
func FileUnlock(file *os.File) error {
	h := syscall.Handle(file.Fd())
	ol := new(syscall.Overlapped)

	r1, _, err := procUnlockFileEx.Call(
		uintptr(h),
		0,
		1, 0,
		uintptr(unsafe.Pointer(ol)),
	)

	if r1 == 0 {
		return err
	}
	return nil
}
