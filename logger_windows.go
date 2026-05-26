//go:build windows
// +build windows

/* ipp-usb - HTTP reverse proxy, backed by IPP-over-USB connection to device
 *
 * See LICENSE for license terms and conditions
 *
 * Logging, system-dependent part for Windows
 */

package main

import (
	"io"
	"os"
	"syscall"
	"unsafe"
)

var (
	kernel32                = syscall.NewLazyDLL("kernel32.dll")
	procGetConsoleMode      = kernel32.NewProc("GetConsoleMode")
	procSetConsoleMode      = kernel32.NewProc("SetConsoleMode")
	procGetConsoleScreenBuf = kernel32.NewProc("GetConsoleScreenBufferInfo")
)

const (
	enableVirtualTerminalProcessing = 0x0004
)

// logIsAtty returns true, if os.File refers to a terminal
func logIsAtty(file *os.File) bool {
	var mode uint32
	h := syscall.Handle(file.Fd())
	r, _, _ := procGetConsoleMode.Call(uintptr(h), uintptr(unsafe.Pointer(&mode)))
	if r == 0 {
		return false
	}

	// Enable ANSI escape sequence processing if available (Windows 10+)
	procSetConsoleMode.Call(uintptr(h),
		uintptr(mode|enableVirtualTerminalProcessing))

	return true
}

// logColorConsoleWrite writes a colorized line to console.
// Uses ANSI escape sequences (supported on Windows 10+ with
// virtual terminal processing enabled).
func logColorConsoleWrite(out io.Writer, level LogLevel, line []byte) {
	var beg, end string

	switch {
	case (level & LogError) != 0:
		beg, end = "\033[31;1m", "\033[0m" // Red
	case (level & LogInfo) != 0:
		beg, end = "\033[32;1m", "\033[0m" // Green
	case (level & LogDebug) != 0:
		beg, end = "\033[37;1m", "\033[0m" // White
	case (level & LogTraceAll) != 0:
		beg, end = "\033[37m", "\033[0m" // Gray
	}

	out.Write([]byte(beg))
	out.Write(line)
	out.Write([]byte(end))
}
