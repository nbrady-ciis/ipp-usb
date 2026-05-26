//go:build (!linux && !freebsd) || noavahi
// +build !linux,!freebsd noavahi

/* ipp-usb - HTTP reverse proxy, backed by IPP-over-USB connection to device
 *
 * Copyright (C) 2020 and up by Alexander Pevzner (pzz@apevzner.com)
 * See LICENSE for license terms and conditions
 *
 * DNS-SD publisher: no-op implementation for platforms without Avahi
 */

package main

// dnssdSysdep is a no-op DNS-SD implementation for platforms without Avahi.
// It satisfies the interface consumed by dnssd.go (Halt, Chan).
type dnssdSysdep struct {
	statusChan chan DNSSdStatus
}

func newDnssdSysdep(log *Logger, instance string,
	services DNSSdServices) *dnssdSysdep {
	return &dnssdSysdep{
		statusChan: make(chan DNSSdStatus),
	}
}

func (sysdep *dnssdSysdep) Halt() {}

func (sysdep *dnssdSysdep) Chan() <-chan DNSSdStatus {
	return sysdep.statusChan
}
