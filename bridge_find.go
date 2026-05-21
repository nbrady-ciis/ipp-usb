/* ipp-usb - HTTP reverse proxy, backed by IPP-over-USB connection to device
 *
 * Copyright (C) 2020 and up by Alexander Pevzner (pzz@apevzner.com)
 * See LICENSE for license terms and conditions
 *
 * Bridge mode: find a specific USB device by VID:PID:serial
 */

package main

import (
	"fmt"
	"strconv"
	"strings"
)

// BridgeFindDevice enumerates USB devices and returns the descriptor
// matching the given VID, PID, and serial number.
// All three parameters are required to prevent ambiguous device selection
// when multiple printers with the same VID:PID are connected.
//
// Note: UsbGetIppOverUsbDeviceDescs() returns map[UsbAddr]UsbDeviceDesc,
// so iteration order is non-deterministic. This is fine since we match
// by VID:PID:serial which is a unique key.
func BridgeFindDevice(vidHex, pidHex, serial string) (UsbDeviceDesc, error) {
	vid, err := strconv.ParseUint(vidHex, 16, 16)
	if err != nil {
		return UsbDeviceDesc{}, fmt.Errorf("invalid VID %q: %s", vidHex, err)
	}

	pid, err := strconv.ParseUint(pidHex, 16, 16)
	if err != nil {
		return UsbDeviceDesc{}, fmt.Errorf("invalid PID %q: %s", pidHex, err)
	}

	descs, err := UsbGetIppOverUsbDeviceDescs()
	if err != nil {
		return UsbDeviceDesc{}, err
	}

	for _, desc := range descs {
		if desc.Vendor == uint16(vid) && desc.Product == uint16(pid) {
			// Must open device briefly to read serial number string
			info, err := desc.GetUsbDeviceInfo()
			if err != nil {
				continue
			}
			if strings.EqualFold(info.SerialNumber, serial) {
				return desc, nil
			}
		}
	}

	return UsbDeviceDesc{}, fmt.Errorf(
		"no IPP-over-USB device with VID=%s PID=%s serial=%q", vidHex, pidHex, serial)
}
