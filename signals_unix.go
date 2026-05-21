//go:build darwin || dragonfly || freebsd || linux || nacl || netbsd || openbsd || solaris
// +build darwin dragonfly freebsd linux nacl netbsd openbsd solaris

/* ipp-usb - HTTP reverse proxy, backed by IPP-over-USB connection to device
 *
 * Copyright (C) 2020 and up by Alexander Pevzner (pzz@apevzner.com)
 * See LICENSE for license terms and conditions
 *
 * Platform-specific signal sets for Unix
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
// Includes SIGHUP for config reload semantics on Unix.
func pnpSignals() []os.Signal {
	return []os.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP}
}
