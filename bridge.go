/* ipp-usb - HTTP reverse proxy, backed by IPP-over-USB connection to device
 *
 * Copyright (C) 2020 and up by Alexander Pevzner (pzz@apevzner.com)
 * See LICENSE for license terms and conditions
 *
 * Bridge mode for single-device HTTP-to-USB proxy
 */

package main

import (
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"runtime"
	"strings"
)

// BridgeParams holds bridge mode parameters
type BridgeParams struct {
	VID      string
	PID      string
	Serial   string
	Port     int
	LogDir   string
	LogLevel string
}

// RunBridge is the entry point for bridge mode
func RunBridge(args []string) {
	params := parseBridgeArgs(args)

	// Mark bridge mode globally (disables auth, Host redirect, etc.)
	Conf.BridgeMode = true

	// Initialize paths (for quirks directory resolution)
	if err := PathsInit(); err != nil {
		bridgeError("path init: %s", err)
	}

	// Override logging directory before anything writes logs
	if params.LogDir != "" {
		PathLogDir = params.LogDir
	}

	// Load configuration + quirks (quirks are essential for device handling)
	if err := ConfLoad(); err != nil {
		bridgeError("config load: %s", err)
	}

	// Configure logging levels for bridge mode
	configureBridgeLogging(params)

	// Check root privileges on Unix (libusb requires it)
	if runtime.GOOS != "windows" && os.Geteuid() != 0 {
		bridgeError("bridge mode requires root privileges on %s", runtime.GOOS)
	}

	// Initialize libusb (no hotplug)
	if err := UsbInit(true); err != nil {
		bridgeError("USB init: %s", err)
	}

	// Find the target device by VID:PID:serial
	desc, err := BridgeFindDevice(params.VID, params.PID, params.Serial)
	if err != nil {
		bridgeError("device not found: %s", err)
	}

	// Create USB transport (opens device, detaches kernel driver, claims interfaces)
	transport, err := NewUsbTransport(desc)
	if err != nil {
		bridgeError("USB transport: %s", err)
	}

	// Create TCP listener on loopback.
	// Port 0 = OS assigns an ephemeral port (eliminates TOCTOU race with caller).
	// If caller specifies --port, use that instead (for debugging/testing).
	addr := fmt.Sprintf("127.0.0.1:%d", params.Port)
	listener, err := net.Listen("tcp4", addr)
	if err != nil {
		transport.Close(false)
		bridgeError("listen on %s: %s", addr, err)
	}

	// Read the actual port from the listener (important when --port 0)
	actualPort := listener.Addr().(*net.TCPAddr).Port

	// Create HTTP proxy
	proxy := NewHTTPProxy(transport.Log(), listener, transport)
	proxy.Enable()

	// Signal readiness with the actual port.
	// Caller parses "READY <port>" to learn where to send HTTP traffic.
	fmt.Fprintf(os.Stdout, "READY %d\n", actualPort)
	os.Stdout.Sync()

	// Wait for termination: signal OR stdin closure (whichever comes first)
	shutdown := make(chan struct{}, 1)

	// Monitor OS signals (SIGINT, SIGTERM)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, bridgeSignals()...)
	go func() {
		<-sig
		select {
		case shutdown <- struct{}{}:
		default:
		}
	}()

	// Monitor stdin closure — primary shutdown mechanism on Windows where
	// Java's Process.destroy() calls TerminateProcess (no signal delivery).
	// Also handles parent process crash on all platforms.
	go func() {
		io.Copy(io.Discard, os.Stdin) // blocks until stdin is closed (EOF)
		select {
		case shutdown <- struct{}{}:
		default:
		}
	}()

	<-shutdown

	// Graceful shutdown
	proxy.Close()
	transport.Close(false)

	fmt.Fprintln(os.Stdout, "SHUTDOWN")
}

func parseBridgeArgs(args []string) BridgeParams {
	fs := flag.NewFlagSet("bridge", flag.ExitOnError)
	params := BridgeParams{}
	fs.StringVar(&params.VID, "vid", "", "USB Vendor ID (hex)")
	fs.StringVar(&params.PID, "pid", "", "USB Product ID (hex)")
	fs.StringVar(&params.Serial, "serial", "", "USB serial number")
	fs.IntVar(&params.Port, "port", 0, "HTTP server port (0 = OS-assigned)")
	fs.StringVar(&params.LogDir, "log-dir", "", "Log directory path")
	fs.StringVar(&params.LogLevel, "log-level", "trace-ipp", "Log level")
	fs.Parse(args)

	if params.VID == "" || params.PID == "" {
		bridgeError("--vid and --pid are required")
	}
	if params.Serial == "" {
		bridgeError("--serial is required (prevents selecting wrong device when multiple share VID:PID)")
	}
	return params
}

func bridgeError(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(os.Stdout, "ERROR %s\n", msg)
	os.Exit(1)
}

func configureBridgeLogging(params BridgeParams) {
	// In bridge mode, stdout is reserved for the protocol (READY/ERROR/SHUTDOWN).
	// Redirect Console logger to stderr so log output doesn't interfere.
	Console.out = os.Stderr

	// Set log levels based on params.LogLevel
	level := parseBridgeLogLevel(params.LogLevel)
	Log.SetLevels(level)
	Console.SetLevels(level)
	Log.Cc(Console)
}

func parseBridgeLogLevel(s string) LogLevel {
	switch strings.ToLower(s) {
	case "error":
		return LogError
	case "info":
		return LogError | LogInfo
	case "debug":
		return LogError | LogInfo | LogDebug
	case "trace-ipp":
		return LogError | LogInfo | LogDebug | LogTraceIPP
	case "trace-usb":
		return LogAll
	default:
		return LogError | LogInfo | LogDebug | LogTraceIPP
	}
}
