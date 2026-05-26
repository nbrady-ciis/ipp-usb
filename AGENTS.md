# AGENTS.md

## Project

ipp-usb — HTTP reverse proxy for IPP-over-USB printers. Single-binary Go daemon with cgo dependencies (libusb, optionally libavahi-common/libavahi-client). Includes a **bridge mode** for use as a subprocess by the Mopria Certification Tool (MCT).

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

### Build Tags

| Tag | Purpose |
|-----|---------|
| `nethttpomithttp2` | Excludes HTTP/2 (always used) |
| `noavahi` | Excludes Avahi DNS-SD and skips `pkg-config: libusb-1.0`. Required for cross-compilation from macOS. When set, `CGO_CFLAGS`/`CGO_LDFLAGS` must be provided explicitly. |
| `integration` | Enables hardware integration tests (bridge mode, requires USB printer + root) |

### Bridge Mode Cross-Compilation

```sh
./build-bridge.sh          # Build all 5 platform targets
./build-bridge.sh darwin   # macOS only (arm64 + amd64)
./build-bridge.sh linux    # Linux only (amd64 + arm64, static musl)
./build-bridge.sh windows  # Windows only (amd64)
./build-bridge.sh clean    # Remove build artifacts
```

Requires: Go 1.17+, zig (`brew install zig`), p7zip (`brew install p7zip` for Windows target).

Output goes to `dist/<platform>/ipp-usb-bridge[.exe]` plus `dist/ipp-usb-quirks/`.

The build script uses `-tags "nethttpomithttp2,noavahi"` and provides explicit `CGO_CFLAGS`/`CGO_LDFLAGS` per target. It builds libusb from source for Linux targets, uses native libusb for macOS, and downloads pre-built MinGW64 static libraries for Windows.

### Integration Tests

Require a physical IPP-over-USB printer and root access. Gated by the `integration` build tag (never run in normal `go test`). Tests skip gracefully if no printer is found.

```sh
# All bridge integration tests:
sudo go test -v -tags "nethttpomithttp2,noavahi,integration" -mod=vendor -run TestBridge -count=1

# Lifecycle tests only (ready signaling, shutdown triggers):
sudo go test -v -tags "nethttpomithttp2,noavahi,integration" -mod=vendor \
    -run "TestBridge(Ready|Shutdown|GracefulDrain)" -count=1

# Discovery only (no printing):
sudo go test -v -tags "nethttpomithttp2,noavahi,integration" -mod=vendor \
    -run TestBridgeDiscovery -count=1
```

Set `IPP_USB_BRIDGE_BIN` env var to point at the bridge binary for out-of-process lifecycle tests (defaults to `./ipp-usb`).

## Architecture

- **Single Go package** (`package main`) at the repo root. No sub-packages.
- Platform-specific code uses build-constraint suffixes: `_unix.go`, `_windows.go`, `_linux.go`, `_other.go`, `_stub.go`.
- `vendor/` contains the sole Go dependency (`github.com/OpenPrinting/goipp`).
- `ipp-usb-quirks/` — INI-format device workaround files keyed by USB VID/PID.
- `systemd-udev/` — udev rules and systemd service units.

### Platform Files

| File | Platforms | Purpose |
|------|-----------|---------|
| `signals_unix.go` | Unix | `bridgeSignals()` (SIGINT, SIGTERM), `pnpSignals()` (+SIGHUP) |
| `signals_windows.go` | Windows | Same functions, no SIGHUP |
| `flock_unix.go` | Unix | File locking via `flock(2)` (cgo) |
| `flock_windows.go` | Windows | File locking via `LockFileEx`/`UnlockFileEx` |
| `logger_unix.go` | Unix | TTY detection via `isatty(3)` (cgo), ANSI color output |
| `logger_windows.go` | Windows | TTY detection via `GetConsoleMode`, enables VT processing |
| `daemon.go` | Unix | `CloseStdInOutErr()` via `dup2`, `Daemon()` for background fork |
| `daemon_windows.go` | Windows | Stubs (bridge runs foreground only) |
| `paths_windows.go` | Windows | Default paths under `%ProgramData%\ipp-usb\` |
| `dnssd_avahi.go` | Linux/FreeBSD (no `noavahi`) | Avahi-based DNS-SD (cgo, pkg-config) |
| `dnssd_stub.go` | All others, or any platform with `noavahi` | No-op DNS-SD |
| `tcpuid_linux.go` | Linux | TCP connection UID lookup |
| `tcpuid_other.go` | Non-Linux | Stub UID lookup |

### Bridge Mode Files

| File | Purpose |
|------|---------|
| `bridge.go` | Entry point (`RunBridge`), CLI parsing, lifecycle orchestration |
| `bridge_find.go` | `BridgeFindDevice()` — locate USB device by VID:PID:serial |
| `bridge_integration_test.go` | Integration tests (build tag: `integration`) |
| `build-bridge.sh` | Cross-compilation script for all 5 platforms |

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
- `pnp.go` — Uses `pnpSignals()` from platform-specific signals file
- `conf.go` — `BridgeMode bool` field on `Configuration` struct
- `http.go` — Auth and Host-redirect guarded by `if !Conf.BridgeMode`
- `usbio_libusb.go` — `#cgo !noavahi pkg-config: libusb-1.0` (conditional on tag)

## Conventions

- Minimum Go version: 1.17 (`go.mod` declares `go 1.17`; `//go:build` syntax used throughout).
- No linter or formatter is enforced in CI; standard `gofmt` style expected.
- No CI runs the Go build or tests — only container packaging workflows exist.
- Man page source is `ipp-usb.8.md` (ronn markdown); regenerate with `make man` (requires `ronn`).
- LSP may report false errors for `linux/amd64` target on macOS — these come from cgo-dependent files (`usbio_libusb.go`, `flock_unix.go`, `daemon.go`) that require platform headers. The actual build uses the correct platform tags.
