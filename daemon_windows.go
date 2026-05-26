//go:build windows
// +build windows

/* ipp-usb - HTTP reverse proxy, backed by IPP-over-USB connection to device
 *
 * See LICENSE for license terms and conditions
 *
 * Daemonization -- Windows version (stubs)
 */

package main

import (
	"errors"
)

// CloseStdInOutErr is a no-op on Windows.
// In bridge mode, stdout is used for the READY/SHUTDOWN protocol and must
// remain open. In daemon mode (not supported on Windows), this would
// redirect stdio to NUL.
func CloseStdInOutErr() error {
	return nil
}

// Daemon is not supported on Windows. The bridge runs in the foreground
// and MCT manages the process lifecycle directly.
func Daemon() error {
	return errors.New("background daemon mode is not supported on Windows")
}
