//go:build windows
// +build windows

/* ipp-usb - HTTP reverse proxy, backed by IPP-over-USB connection to device
 *
 * See LICENSE for license terms and conditions
 *
 * Platform-specific signal sets for Windows
 */

package main

import (
	"os"
	"syscall"
)

// bridgeSignals returns the signals that trigger bridge shutdown.
func bridgeSignals() []os.Signal {
	return []os.Signal{syscall.SIGINT, syscall.SIGTERM}
}

// pnpSignals returns the signals that trigger PnP manager shutdown.
// On Windows there is no SIGHUP, so only SIGINT and SIGTERM are used.
func pnpSignals() []os.Signal {
	return []os.Signal{syscall.SIGINT, syscall.SIGTERM}
}
