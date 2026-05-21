# AGENTS.md

## Project

ipp-usb — HTTP reverse proxy for IPP-over-USB printers. Single-binary Go daemon with cgo dependencies (libusb, libavahi-common, libavahi-client). Includes a **bridge mode** for use as a subprocess by the Mopria Certification Tool (MCT).

## Build & Test

```sh
make          # build binary (requires ctags, libusb, avahi dev headers)
make test     # run tests
```

All `go` commands **must** use `-mod=vendor`. The dependency is vendored and there is no module proxy expectation.

The build requires the tag `nethttpomithttp2` (HTTP/2 deliberately excluded):
```sh
go build -tags nethttpomithttp2 -mod=vendor
go test -tags nethttpomithttp2 -mod=vendor
```

### Bridge Mode Cross-Compilation

```sh
./build-bridge.sh          # Build all 5 platform targets
./build-bridge.sh darwin   # macOS only (arm64 + amd64)
./build-bridge.sh linux    # Linux only (amd64 + arm64, static musl)
./build-bridge.sh windows  # Windows only (amd64)
./build-bridge.sh clean    # Remove build artifacts
```

Requires: Go 1.19+, zig (`brew install zig`), p7zip (`brew install p7zip` for Windows target).

Output goes to `dist/<platform>/ipp-usb-bridge[.exe]` plus `dist/ipp-usb-quirks/`.

### Integration Tests

Require a physical IPP-over-USB printer and root access. Gated by the `integration` build tag (never run in normal `go test`). Tests skip gracefully if no printer is found.

```sh
# All bridge integration tests:
sudo go test -v -tags "nethttpomithttp2,integration" -mod=vendor -run TestBridge -count=1

# Lifecycle tests only (ready signaling, shutdown triggers):
sudo go test -v -tags "nethttpomithttp2,integration" -mod=vendor \
    -run "TestBridge(Ready|Shutdown|GracefulDrain)" -count=1

# Discovery only (no printing):
sudo go test -v -tags "nethttpomithttp2,integration" -mod=vendor \
    -run TestBridgeDiscovery -count=1
```

Set `IPP_USB_BRIDGE_BIN` env var to point at the bridge binary for out-of-process lifecycle tests (defaults to `./ipp-usb`).

## Architecture

- **Single Go package** (`package main`) at the repo root. No sub-packages.
- Platform-specific code uses build-constraint suffixes: `_linux.go`, `_unix.go`, `_other.go`, `_stub.go`.
- `vendor/` contains the sole Go dependency (`github.com/OpenPrinting/goipp`).
- `ipp-usb-quirks/` — INI-format device workaround files keyed by USB VID/PID.
- `systemd-udev/` — udev rules and systemd service units.

### Bridge Mode Files

| File | Purpose |
|------|---------|
| `bridge.go` | Entry point (`RunBridge`), CLI parsing, lifecycle orchestration |
| `bridge_find.go` | `BridgeFindDevice()` — locate USB device by VID:PID:serial |
| `dnssd_stub.go` | No-op DNS-SD for non-Linux/FreeBSD (build tag: `!linux && !freebsd`) |
| `signals_unix.go` | `bridgeSignals()` and `pnpSignals()` for Unix platforms |
| `bridge_integration_test.go` | Integration tests (build tag: `integration`) |
| `build-bridge.sh` | Cross-compilation script for all platforms |

### Bridge Mode Behavior

```
$ sudo ./ipp-usb bridge --vid 04f9 --pid 0027 --serial XYZ123 --log-dir /tmp/test
READY 52718        # device opened, server listening on this port
... serves HTTP ...
SHUTDOWN           # clean exit
```

**Stdout protocol:** `READY <port>`, `ERROR <message>`, `SHUTDOWN`

**Shutdown triggers:** SIGTERM/SIGINT (Unix), stdin EOF (all platforms — primary mechanism for MCT subprocess control).

**Exit codes:** 0 = clean shutdown, 1 = startup failure, 2 = runtime error.

**What bridge mode skips:** PnP hotplug, DNS-SD advertising, file lock, daemon fork, control socket, root check enforcement, auth.

### Key Modifications for Bridge Mode

- `main.go` — Early dispatch: `os.Args[1] == "bridge"` before `PathsInit`/`parseArgv`
- `pnp.go` — Uses `pnpSignals()` from `signals_unix.go` (removed direct `syscall.SIGHUP`)
- `conf.go` — `BridgeMode bool` field on `Configuration` struct
- `http.go` — Auth and Host-redirect guarded by `if !Conf.BridgeMode`

## Conventions

- Minimum Go version: 1.11 (avoid features unavailable before Go 1.11).
- No linter or formatter is enforced in CI; standard `gofmt` style expected.
- No CI runs the Go build or tests — only container packaging workflows exist.
- Man page source is `ipp-usb.8.md` (ronn markdown); regenerate with `make man` (requires `ronn`).
- LSP may report false errors for `linux/amd64` target on macOS — these come from cgo-dependent files (`usbio_libusb.go`, `flock_unix.go`, `daemon.go`) that require platform headers. The actual build uses the correct platform tags.
