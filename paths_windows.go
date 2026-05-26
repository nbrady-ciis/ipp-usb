//go:build windows
// +build windows

/* ipp-usb - HTTP reverse proxy, backed by IPP-over-USB connection to device
 *
 * See LICENSE for license terms and conditions
 *
 * Common paths -- Windows overrides
 */

package main

import (
	"os"
	"path/filepath"
)

func init() {
	programData := os.Getenv("ProgramData")
	if programData == "" {
		programData = `C:\ProgramData`
	}
	base := filepath.Join(programData, "ipp-usb")

	// Override the default var values from paths.go with
	// Windows-appropriate locations
	PathControlSocket = filepath.Join(base, "ctrl")
	PathLockFile = filepath.Join(base, "lock", "ipp-usb.lock")
	PathLogDir = filepath.Join(base, "log")
	PathDevStateDir = filepath.Join(base, "dev")
}
