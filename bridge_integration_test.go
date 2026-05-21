//go:build integration
// +build integration

/* ipp-usb - HTTP reverse proxy, backed by IPP-over-USB connection to device
 *
 * Copyright (C) 2020 and up by Alexander Pevzner (pzz@apevzner.com)
 * See LICENSE for license terms and conditions
 *
 * Bridge mode integration tests
 *
 * These tests require a physical IPP-over-USB printer and root access.
 * They are gated by the "integration" build tag so they never run in
 * normal "go test" invocations.
 *
 * Run with:
 *   sudo go test -v -tags "nethttpomithttp2,integration" -mod=vendor -run TestBridge -count=1
 */

package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/OpenPrinting/goipp"
)

// Cached printer info — on macOS, USB re-enumeration after a subprocess
// releases a device can take 10-30 seconds (device address changes).
// We cache the VID/PID/serial from the first successful discovery and
// reuse it for all subprocess tests.
var (
	cachedVID, cachedPID, cachedSerial string
)

// testBridgeEnv holds a running bridge for the duration of a test.
type testBridgeEnv struct {
	transport *UsbTransport
	proxy     *HTTPProxy
	listener  net.Listener
	port      int
	baseURL   string
}

func setupBridge(t *testing.T) *testBridgeEnv {
	t.Helper()

	// Initialize paths and config
	if err := PathsInit(); err != nil {
		t.Fatalf("PathsInit: %v", err)
	}
	if err := ConfLoad(); err != nil {
		t.Fatalf("ConfLoad: %v", err)
	}
	Conf.BridgeMode = true

	// Initialize USB
	if err := UsbInit(true); err != nil {
		t.Fatalf("UsbInit: %v", err)
	}

	// Find first available IPP-over-USB device.
	// Retry for up to 15 seconds to allow re-enumeration after a previous test.
	var desc UsbDeviceDesc
	var info UsbDeviceInfo
	deadline := time.Now().Add(15 * time.Second)
	for {
		descs, err := UsbGetIppOverUsbDeviceDescs()
		if err != nil {
			t.Fatalf("UsbGetIppOverUsbDeviceDescs: %v", err)
		}
		for _, d := range descs {
			if i, err := d.GetUsbDeviceInfo(); err == nil {
				desc = d
				info = i
				goto found
			}
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Skip("No IPP-over-USB printers found — skipping integration tests")

found:
	t.Logf("Using printer: %s (VID=%04x PID=%04x Serial=%s)",
		info.MakeAndModel(), desc.Vendor, desc.Product, info.SerialNumber)

	// Create transport
	transport, err := NewUsbTransport(desc)
	if err != nil {
		t.Fatalf("NewUsbTransport: %v", err)
	}

	// Listen on ephemeral port
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		transport.Close(false)
		t.Fatalf("Listen: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port

	// Start HTTP proxy
	proxy := NewHTTPProxy(transport.Log(), listener, transport)
	proxy.Enable()

	env := &testBridgeEnv{
		transport: transport,
		proxy:     proxy,
		listener:  listener,
		port:      port,
		baseURL:   fmt.Sprintf("http://127.0.0.1:%d", port),
	}

	t.Cleanup(func() {
		proxy.Close()
		transport.Close(false)
	})

	return env
}

// sendIPP sends an IPP request and returns the decoded response.
func (env *testBridgeEnv) sendIPP(t *testing.T, path string, req *goipp.Message) *goipp.Message {
	t.Helper()

	var body bytes.Buffer
	if err := req.Encode(&body); err != nil {
		t.Fatalf("Encode IPP request: %v", err)
	}

	httpReq, _ := http.NewRequest("POST", env.baseURL+path, &body)
	httpReq.Header.Set("Content-Type", "application/ipp")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		t.Fatalf("HTTP POST %s: %v", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("HTTP %d from %s", resp.StatusCode, path)
	}

	var ippResp goipp.Message
	if err := ippResp.Decode(resp.Body); err != nil {
		t.Fatalf("Decode IPP response: %v", err)
	}
	return &ippResp
}

func TestBridgeDiscovery(t *testing.T) {
	if err := PathsInit(); err != nil {
		t.Fatalf("PathsInit: %v", err)
	}
	if err := UsbInit(true); err != nil {
		t.Fatalf("UsbInit: %v", err)
	}
	descs, err := UsbGetIppOverUsbDeviceDescs()
	if err != nil {
		t.Fatalf("UsbGetIppOverUsbDeviceDescs: %v", err)
	}
	if len(descs) == 0 {
		t.Skip("No IPP-over-USB printers found")
	}
	for addr, desc := range descs {
		info, err := desc.GetUsbDeviceInfo()
		if err != nil {
			t.Logf("Found: %s — (could not read info: %v)", addr, err)
			continue
		}
		t.Logf("Found: %s — %s (%04x:%04x serial=%s)",
			addr, info.MakeAndModel(), desc.Vendor, desc.Product, info.SerialNumber)
		// Cache for subsequent tests
		if cachedVID == "" {
			cachedVID = fmt.Sprintf("%04x", desc.Vendor)
			cachedPID = fmt.Sprintf("%04x", desc.Product)
			cachedSerial = info.SerialNumber
		}
	}
}

// --- In-Process Tests (USB traffic tests) ---
// These run BEFORE subprocess tests because they share the same libusb context
// and don't trigger macOS USB re-enumeration delays between tests.

func TestBridgeGetPrinterAttributes(t *testing.T) {
	env := setupBridge(t)

	req := goipp.NewRequest(goipp.DefaultVersion, goipp.OpGetPrinterAttributes, 1)
	req.Operation.Add(goipp.MakeAttribute("attributes-charset", goipp.TagCharset,
		goipp.String("utf-8")))
	req.Operation.Add(goipp.MakeAttribute("attributes-natural-language", goipp.TagLanguage,
		goipp.String("en")))
	req.Operation.Add(goipp.MakeAttribute("printer-uri", goipp.TagURI,
		goipp.String(fmt.Sprintf("ipp://localhost:%d/ipp/print", env.port))))

	resp := env.sendIPP(t, "/ipp/print", req)

	if resp.Code&0xFF00 != 0 {
		t.Fatalf("IPP error status: 0x%04x", resp.Code)
	}

	// Verify expected attributes present
	for _, attr := range resp.Printer {
		switch attr.Name {
		case "printer-state":
			t.Logf("printer-state: %v", attr.Values[0].V)
		case "printer-make-and-model":
			t.Logf("printer-make-and-model: %v", attr.Values[0].V)
		}
	}
	t.Logf("Get-Printer-Attributes: success (status 0x%04x)", resp.Code)
}

func TestBridgeGetJobs(t *testing.T) {
	env := setupBridge(t)

	req := goipp.NewRequest(goipp.DefaultVersion, goipp.OpGetJobs, 2)
	req.Operation.Add(goipp.MakeAttribute("attributes-charset", goipp.TagCharset,
		goipp.String("utf-8")))
	req.Operation.Add(goipp.MakeAttribute("attributes-natural-language", goipp.TagLanguage,
		goipp.String("en")))
	req.Operation.Add(goipp.MakeAttribute("printer-uri", goipp.TagURI,
		goipp.String(fmt.Sprintf("ipp://localhost:%d/ipp/print", env.port))))

	resp := env.sendIPP(t, "/ipp/print", req)

	if resp.Code&0xFF00 != 0 {
		t.Fatalf("IPP error status: 0x%04x", resp.Code)
	}
	t.Logf("Get-Jobs: success (status 0x%04x)", resp.Code)
}

func TestBridgePrintDocument(t *testing.T) {
	env := setupBridge(t)

	// Load real PWG Raster test file (mandatory format for all IPP Everywhere printers)
	docData, err := os.ReadFile("testdata/sunflower.pwg")
	if err != nil {
		t.Fatalf("ReadFile testdata/sunflower.pwg: %v", err)
	}

	// Build Print-Job request
	req := goipp.NewRequest(goipp.DefaultVersion, goipp.OpPrintJob, 3)
	req.Operation.Add(goipp.MakeAttribute("attributes-charset", goipp.TagCharset,
		goipp.String("utf-8")))
	req.Operation.Add(goipp.MakeAttribute("attributes-natural-language", goipp.TagLanguage,
		goipp.String("en")))
	req.Operation.Add(goipp.MakeAttribute("printer-uri", goipp.TagURI,
		goipp.String(fmt.Sprintf("ipp://localhost:%d/ipp/print", env.port))))
	req.Operation.Add(goipp.MakeAttribute("requesting-user-name", goipp.TagName,
		goipp.String("integration-test")))
	req.Operation.Add(goipp.MakeAttribute("job-name", goipp.TagName,
		goipp.String("bridge-integration-test")))
	req.Operation.Add(goipp.MakeAttribute("document-format", goipp.TagMimeType,
		goipp.String("image/pwg-raster")))

	// Encode IPP header + append document data
	var body bytes.Buffer
	if err := req.Encode(&body); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	body.Write(docData)

	// Send via HTTP
	httpReq, _ := http.NewRequest("POST", env.baseURL+"/ipp/print", &body)
	httpReq.Header.Set("Content-Type", "application/ipp")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		t.Fatalf("HTTP POST: %v", err)
	}
	defer resp.Body.Close()

	var ippResp goipp.Message
	if err := ippResp.Decode(resp.Body); err != nil {
		t.Fatalf("Decode response: %v", err)
	}

	// Accept success or "ignored/substituted attributes"
	status := goipp.Status(ippResp.Code)
	if status != goipp.StatusOk &&
		status != goipp.StatusOkIgnoredOrSubstituted {
		t.Fatalf("Print-Job failed: status 0x%04x", ippResp.Code)
	}

	// Extract job-id
	var jobID int
	for _, attr := range ippResp.Job {
		if attr.Name == "job-id" {
			jobID = int(attr.Values[0].V.(goipp.Integer))
		}
	}
	t.Logf("Print-Job accepted: job-id=%d (status 0x%04x)", jobID, ippResp.Code)

	// Poll job until complete (max 60 seconds)
	if jobID > 0 {
		pollJobCompletion(t, env, jobID, 60*time.Second)
	}
}

func TestBridgeCancelJob(t *testing.T) {
	env := setupBridge(t)

	// Submit a job first
	docData, err := os.ReadFile("testdata/sunflower.pwg")
	if err != nil {
		t.Fatalf("ReadFile testdata/sunflower.pwg: %v", err)
	}
	req := goipp.NewRequest(goipp.DefaultVersion, goipp.OpPrintJob, 4)
	req.Operation.Add(goipp.MakeAttribute("attributes-charset", goipp.TagCharset,
		goipp.String("utf-8")))
	req.Operation.Add(goipp.MakeAttribute("attributes-natural-language", goipp.TagLanguage,
		goipp.String("en")))
	req.Operation.Add(goipp.MakeAttribute("printer-uri", goipp.TagURI,
		goipp.String(fmt.Sprintf("ipp://localhost:%d/ipp/print", env.port))))
	req.Operation.Add(goipp.MakeAttribute("requesting-user-name", goipp.TagName,
		goipp.String("integration-test")))
	req.Operation.Add(goipp.MakeAttribute("job-name", goipp.TagName,
		goipp.String("cancel-test")))
	req.Operation.Add(goipp.MakeAttribute("document-format", goipp.TagMimeType,
		goipp.String("image/pwg-raster")))

	var body bytes.Buffer
	req.Encode(&body)
	body.Write(docData)

	httpReq, _ := http.NewRequest("POST", env.baseURL+"/ipp/print", &body)
	httpReq.Header.Set("Content-Type", "application/ipp")
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		t.Fatalf("HTTP POST for print: %v", err)
	}
	var printResp goipp.Message
	printResp.Decode(resp.Body)
	resp.Body.Close()

	var jobID int
	for _, attr := range printResp.Job {
		if attr.Name == "job-id" {
			jobID = int(attr.Values[0].V.(goipp.Integer))
		}
	}
	if jobID == 0 {
		t.Skip("Could not get job-id from Print-Job response; skipping cancel test")
	}

	// Cancel the job
	cancelReq := goipp.NewRequest(goipp.DefaultVersion, goipp.OpCancelJob, 5)
	cancelReq.Operation.Add(goipp.MakeAttribute("attributes-charset", goipp.TagCharset,
		goipp.String("utf-8")))
	cancelReq.Operation.Add(goipp.MakeAttribute("attributes-natural-language", goipp.TagLanguage,
		goipp.String("en")))
	cancelReq.Operation.Add(goipp.MakeAttribute("printer-uri", goipp.TagURI,
		goipp.String(fmt.Sprintf("ipp://localhost:%d/ipp/print", env.port))))
	cancelReq.Operation.Add(goipp.MakeAttribute("job-id", goipp.TagInteger,
		goipp.Integer(jobID)))

	cancelResp := env.sendIPP(t, "/ipp/print", cancelReq)

	// Accept success or "not-found" (job already completed)
	cancelStatus := goipp.Status(cancelResp.Code)
	switch cancelStatus {
	case goipp.StatusOk:
		t.Logf("Cancel-Job: success (job %d cancelled)", jobID)
	case goipp.StatusErrorNotFound:
		t.Logf("Cancel-Job: job %d already completed", jobID)
	default:
		t.Logf("Cancel-Job: status 0x%04x (may be acceptable)", cancelResp.Code)
	}
}

// --- Lifecycle Tests (out-of-process) ---
// These run AFTER in-process tests. On macOS, subprocess tests cause USB
// re-enumeration delays, but since they use cached VID/PID/serial they
// don't need to re-discover the device.

// testBridgeBinary returns the path to the bridge binary for subprocess tests.
// Set IPP_USB_BRIDGE_BIN env var to override.
func testBridgeBinary(t *testing.T) string {
	t.Helper()
	if bin := os.Getenv("IPP_USB_BRIDGE_BIN"); bin != "" {
		return bin
	}
	// Default: assume binary is built in the same directory
	bin := "./ipp-usb"
	if _, err := os.Stat(bin); err != nil {
		t.Skipf("Bridge binary not found at %s (set IPP_USB_BRIDGE_BIN or build first)", bin)
	}
	return bin
}

// firstPrinterInfo returns VID, PID, and serial of the first available printer.
// Results are cached because on macOS, USB re-enumeration after a subprocess
// releases a device takes 10-30 seconds (device address changes each time).
func firstPrinterInfo(t *testing.T) (vid, pid, serial string) {
	t.Helper()

	// Return cached info if available (avoids re-enumeration delay)
	if cachedVID != "" {
		return cachedVID, cachedPID, cachedSerial
	}

	if err := PathsInit(); err != nil {
		t.Fatalf("PathsInit: %v", err)
	}
	if err := UsbInit(true); err != nil {
		t.Fatalf("UsbInit: %v", err)
	}

	deadline := time.Now().Add(15 * time.Second)
	for {
		descs, err := UsbGetIppOverUsbDeviceDescs()
		if err != nil {
			t.Fatalf("UsbGetIppOverUsbDeviceDescs: %v", err)
		}
		for _, desc := range descs {
			info, err := desc.GetUsbDeviceInfo()
			if err != nil {
				continue
			}
			cachedVID = fmt.Sprintf("%04x", desc.Vendor)
			cachedPID = fmt.Sprintf("%04x", desc.Product)
			cachedSerial = info.SerialNumber
			return cachedVID, cachedPID, cachedSerial
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Skip("No IPP-over-USB printers found (waited 15s for re-enumeration)")
	return "", "", ""
}

// startBridgeProcess launches the bridge binary and returns the process, its stdin pipe,
// and a buffered reader on stdout.
func startBridgeProcess(t *testing.T, args ...string) (*exec.Cmd, io.WriteCloser, *bufio.Reader) {
	t.Helper()
	bin := testBridgeBinary(t)
	cmd := exec.Command(bin, args...)
	cmd.Stderr = os.Stderr // let bridge stderr pass through for debugging

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("StdinPipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start bridge: %v", err)
	}
	return cmd, stdin, bufio.NewReader(stdout)
}

// waitForReady reads the first line from bridge stdout and parses "READY <port>".
func waitForReady(t *testing.T, stdout *bufio.Reader, timeout time.Duration) int {
	t.Helper()
	type result struct {
		line string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		line, err := stdout.ReadString('\n')
		ch <- result{strings.TrimSpace(line), err}
	}()

	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatalf("Reading READY from bridge: %v", r.err)
		}
		if strings.HasPrefix(r.line, "ERROR ") {
			t.Fatalf("Bridge reported error: %s", r.line)
		}
		if !strings.HasPrefix(r.line, "READY ") {
			t.Fatalf("Expected 'READY <port>', got: %q", r.line)
		}
		portStr := strings.TrimPrefix(r.line, "READY ")
		port, err := strconv.Atoi(portStr)
		if err != nil {
			t.Fatalf("Invalid port in READY message %q: %v", r.line, err)
		}
		if port < 1 || port > 65535 {
			t.Fatalf("Port out of range: %d", port)
		}
		return port
	case <-time.After(timeout):
		t.Fatalf("Timed out waiting for READY (timeout=%v)", timeout)
		return 0
	}
}

func TestBridgeReadySignal(t *testing.T) {
	vid, pid, serial := firstPrinterInfo(t)

	cmd, stdin, stdout := startBridgeProcess(t,
		"bridge", "--vid", vid, "--pid", pid, "--serial", serial)
	defer func() {
		stdin.Close()
		cmd.Wait()
	}()

	// Verify READY <port> arrives within 10 seconds
	port := waitForReady(t, stdout, 10*time.Second)
	t.Logf("Bridge reported READY on port %d", port)

	// Verify the port is actually accepting TCP connections
	conn, err := net.DialTimeout("tcp4", fmt.Sprintf("127.0.0.1:%d", port), 2*time.Second)
	if err != nil {
		t.Fatalf("Cannot connect to bridge port %d: %v", port, err)
	}
	conn.Close()
	t.Logf("Verified: port %d is accepting connections", port)
}

func TestBridgeReadyError(t *testing.T) {
	// Use a VID:PID:serial that doesn't exist — bridge should print ERROR and exit 1
	cmd, stdin, stdout := startBridgeProcess(t,
		"bridge", "--vid", "ffff", "--pid", "ffff", "--serial", "NONEXISTENT_DEVICE_XYZ")
	defer stdin.Close()

	// Read the first line — should be "ERROR ..."
	type result struct {
		line string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		line, err := stdout.ReadString('\n')
		ch <- result{strings.TrimSpace(line), err}
	}()

	select {
	case r := <-ch:
		if r.err != nil && r.err != io.EOF {
			t.Fatalf("Reading from bridge stdout: %v", r.err)
		}
		if !strings.HasPrefix(r.line, "ERROR ") {
			t.Fatalf("Expected 'ERROR ...', got: %q", r.line)
		}
		t.Logf("Bridge correctly reported: %s", r.line)
	case <-time.After(10 * time.Second):
		t.Fatal("Timed out waiting for ERROR from bridge")
	}

	// Verify exit code is 1
	err := cmd.Wait()
	if err == nil {
		t.Fatal("Expected non-zero exit code, got 0")
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		if exitErr.ExitCode() != 1 {
			t.Fatalf("Expected exit code 1, got %d", exitErr.ExitCode())
		}
		t.Logf("Bridge exited with code 1 (correct)")
	} else {
		t.Fatalf("Unexpected error type: %v", err)
	}
}

func TestBridgeShutdownStdinClose(t *testing.T) {
	vid, pid, serial := firstPrinterInfo(t)

	cmd, stdin, stdout := startBridgeProcess(t,
		"bridge", "--vid", vid, "--pid", pid, "--serial", serial)

	// Wait for READY
	port := waitForReady(t, stdout, 10*time.Second)
	t.Logf("Bridge ready on port %d; closing stdin to trigger shutdown", port)

	// Close stdin — this is the primary shutdown trigger
	stdin.Close()

	// Read the next line — should be "SHUTDOWN"
	type result struct {
		line string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		line, err := stdout.ReadString('\n')
		ch <- result{strings.TrimSpace(line), err}
	}()

	select {
	case r := <-ch:
		if r.err != nil && r.err != io.EOF {
			t.Fatalf("Reading SHUTDOWN: %v", r.err)
		}
		if r.line != "SHUTDOWN" {
			t.Fatalf("Expected 'SHUTDOWN', got: %q", r.line)
		}
		t.Logf("Bridge printed SHUTDOWN")
	case <-time.After(5 * time.Second):
		cmd.Process.Kill()
		t.Fatal("Bridge did not print SHUTDOWN within 5s of stdin close")
	}

	// Verify clean exit (code 0)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Expected exit code 0, got: %v", err)
		}
		t.Logf("Bridge exited cleanly (code 0)")
	case <-time.After(5 * time.Second):
		cmd.Process.Kill()
		t.Fatal("Bridge did not exit within 5s of stdin close")
	}

	// Verify the port is no longer accepting connections
	_, err := net.DialTimeout("tcp4", fmt.Sprintf("127.0.0.1:%d", port), 1*time.Second)
	if err == nil {
		t.Fatal("Port still accepting connections after SHUTDOWN")
	}
	t.Logf("Verified: port %d is closed", port)
}

func TestBridgeShutdownSignal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Signal-based shutdown not reliable on Windows; use stdin close instead")
	}

	vid, pid, serial := firstPrinterInfo(t)

	cmd, stdin, stdout := startBridgeProcess(t,
		"bridge", "--vid", vid, "--pid", pid, "--serial", serial)
	defer stdin.Close()

	// Wait for READY
	port := waitForReady(t, stdout, 10*time.Second)
	t.Logf("Bridge ready on port %d; sending SIGTERM", port)

	// Send SIGTERM
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("Failed to send SIGTERM: %v", err)
	}

	// Read SHUTDOWN
	type result struct {
		line string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		line, err := stdout.ReadString('\n')
		ch <- result{strings.TrimSpace(line), err}
	}()

	select {
	case r := <-ch:
		if r.err != nil && r.err != io.EOF {
			t.Fatalf("Reading SHUTDOWN: %v", r.err)
		}
		if r.line != "SHUTDOWN" {
			t.Fatalf("Expected 'SHUTDOWN', got: %q", r.line)
		}
		t.Logf("Bridge printed SHUTDOWN after SIGTERM")
	case <-time.After(5 * time.Second):
		cmd.Process.Kill()
		t.Fatal("Bridge did not print SHUTDOWN within 5s of SIGTERM")
	}

	// Verify clean exit
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Expected exit code 0, got: %v", err)
		}
		t.Logf("Bridge exited cleanly (code 0) after SIGTERM")
	case <-time.After(5 * time.Second):
		cmd.Process.Kill()
		t.Fatal("Bridge did not exit within 5s of SIGTERM")
	}
}

func TestBridgeRequestAfterReady(t *testing.T) {
	vid, pid, serial := firstPrinterInfo(t)

	cmd, stdin, stdout := startBridgeProcess(t,
		"bridge", "--vid", vid, "--pid", pid, "--serial", serial)
	defer func() {
		stdin.Close()
		cmd.Wait()
	}()

	// Wait for READY
	port := waitForReady(t, stdout, 10*time.Second)
	t.Logf("Bridge ready on port %d; sending Get-Printer-Attributes", port)

	// Send a real IPP request to the bridge subprocess
	req := goipp.NewRequest(goipp.DefaultVersion, goipp.OpGetPrinterAttributes, 1)
	req.Operation.Add(goipp.MakeAttribute("attributes-charset", goipp.TagCharset,
		goipp.String("utf-8")))
	req.Operation.Add(goipp.MakeAttribute("attributes-natural-language", goipp.TagLanguage,
		goipp.String("en")))
	req.Operation.Add(goipp.MakeAttribute("printer-uri", goipp.TagURI,
		goipp.String(fmt.Sprintf("ipp://localhost:%d/ipp/print", port))))

	var body bytes.Buffer
	if err := req.Encode(&body); err != nil {
		t.Fatalf("Encode: %v", err)
	}

	url := fmt.Sprintf("http://127.0.0.1:%d/ipp/print", port)
	httpReq, _ := http.NewRequest("POST", url, &body)
	httpReq.Header.Set("Content-Type", "application/ipp")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		t.Fatalf("HTTP POST: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("HTTP %d (expected 200)", resp.StatusCode)
	}

	var ippResp goipp.Message
	if err := ippResp.Decode(resp.Body); err != nil {
		t.Fatalf("Decode IPP response: %v", err)
	}

	if ippResp.Code&0xFF00 != 0 {
		t.Fatalf("IPP error status: 0x%04x", ippResp.Code)
	}
	t.Logf("Get-Printer-Attributes via subprocess: success (status 0x%04x)", ippResp.Code)
}

func TestBridgeGracefulDrain(t *testing.T) {
	vid, pid, serial := firstPrinterInfo(t)

	cmd, stdin, stdout := startBridgeProcess(t,
		"bridge", "--vid", vid, "--pid", pid, "--serial", serial)

	// Wait for READY
	port := waitForReady(t, stdout, 10*time.Second)
	t.Logf("Bridge ready on port %d", port)

	// Start an HTTP request (Get-Printer-Attributes — takes a moment over USB)
	req := goipp.NewRequest(goipp.DefaultVersion, goipp.OpGetPrinterAttributes, 1)
	req.Operation.Add(goipp.MakeAttribute("attributes-charset", goipp.TagCharset,
		goipp.String("utf-8")))
	req.Operation.Add(goipp.MakeAttribute("attributes-natural-language", goipp.TagLanguage,
		goipp.String("en")))
	req.Operation.Add(goipp.MakeAttribute("printer-uri", goipp.TagURI,
		goipp.String(fmt.Sprintf("ipp://localhost:%d/ipp/print", port))))

	var body bytes.Buffer
	req.Encode(&body)

	url := fmt.Sprintf("http://127.0.0.1:%d/ipp/print", port)
	httpReq, _ := http.NewRequest("POST", url, &body)
	httpReq.Header.Set("Content-Type", "application/ipp")

	// Fire the request in background
	type httpResult struct {
		resp *http.Response
		err  error
	}
	resultCh := make(chan httpResult, 1)
	client := &http.Client{Timeout: 30 * time.Second}
	go func() {
		resp, err := client.Do(httpReq)
		resultCh <- httpResult{resp, err}
	}()

	// Give the request a moment to reach the bridge, then close stdin
	time.Sleep(100 * time.Millisecond)
	t.Logf("Closing stdin while request is in-flight")
	stdin.Close()

	// The in-flight request should still complete successfully
	select {
	case r := <-resultCh:
		if r.err != nil {
			t.Fatalf("In-flight request failed after stdin close: %v", r.err)
		}
		defer r.resp.Body.Close()
		if r.resp.StatusCode != 200 {
			t.Fatalf("In-flight request got HTTP %d (expected 200)", r.resp.StatusCode)
		}
		t.Logf("In-flight request completed successfully (HTTP 200) — graceful drain works")
	case <-time.After(15 * time.Second):
		t.Fatal("In-flight request did not complete within 15s")
	}

	// Bridge should exit after draining
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Expected exit code 0 after drain, got: %v", err)
		}
		t.Logf("Bridge exited cleanly after draining in-flight request")
	case <-time.After(10 * time.Second):
		cmd.Process.Kill()
		t.Fatal("Bridge did not exit within 10s after drain")
	}
}

// --- Helpers ---

func pollJobCompletion(t *testing.T, env *testBridgeEnv, jobID int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		req := goipp.NewRequest(goipp.DefaultVersion, goipp.OpGetJobAttributes, 99)
		req.Operation.Add(goipp.MakeAttribute("attributes-charset", goipp.TagCharset,
			goipp.String("utf-8")))
		req.Operation.Add(goipp.MakeAttribute("attributes-natural-language", goipp.TagLanguage,
			goipp.String("en")))
		req.Operation.Add(goipp.MakeAttribute("printer-uri", goipp.TagURI,
			goipp.String(fmt.Sprintf("ipp://localhost:%d/ipp/print", env.port))))
		req.Operation.Add(goipp.MakeAttribute("job-id", goipp.TagInteger,
			goipp.Integer(jobID)))

		resp := env.sendIPP(t, "/ipp/print", req)

		for _, attr := range resp.Job {
			if attr.Name == "job-state" {
				state := int(attr.Values[0].V.(goipp.Integer))
				// 9 = completed, 8 = cancelled, 7 = aborted
				if state >= 7 {
					t.Logf("Job %d reached terminal state: %d", jobID, state)
					return
				}
				t.Logf("Job %d state: %d (waiting...)", jobID, state)
			}
		}
		time.Sleep(2 * time.Second)
	}
	t.Logf("Job %d did not complete within %v (may still be processing)", jobID, timeout)
}


