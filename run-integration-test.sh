#!/bin/bash
# run-integration-test.sh — Run bridge mode integration tests.
#
# Requires: root privileges, physical IPP-over-USB printer connected.
# Tests skip gracefully if no printer is found.
#
# Usage:
#   sudo -E ./run-integration-test.sh              # All bridge tests
#   sudo -E ./run-integration-test.sh discovery    # Discovery only
#   sudo -E ./run-integration-test.sh lifecycle    # Ready/shutdown lifecycle
#   sudo -E ./run-integration-test.sh print        # Print + cancel tests
#
# Note: use "sudo -E" to preserve PATH (needed for go, cc toolchain).
# Alternatively, the script attempts to locate go and set PATH automatically.

set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

# When running under sudo, PATH is often reset. Ensure go and cc are findable.
if ! command -v go >/dev/null 2>&1; then
    # Common Go install locations
    for p in /usr/local/go/bin /opt/homebrew/bin /usr/local/bin; do
        [ -x "$p/go" ] && export PATH="$p:$PATH"
    done
fi
if ! command -v go >/dev/null 2>&1; then
    echo "Error: 'go' not found in PATH." >&2
    echo "Run with: sudo -E ./run-integration-test.sh" >&2
    exit 1
fi

# Ensure C compiler is available (Xcode command line tools on macOS)
if ! command -v cc >/dev/null 2>&1; then
    export PATH="/usr/bin:/Library/Developer/CommandLineTools/usr/bin:$PATH"
fi

TAGS="nethttpomithttp2,integration"

if [ "$(id -u)" -ne 0 ]; then
    echo "Error: integration tests require root (USB device access)" >&2
    echo "Usage: sudo -E $0 [discovery|lifecycle|print]" >&2
    exit 1
fi

# Build the binary first (needed for out-of-process lifecycle tests)
echo "Building ipp-usb..."
go build -tags nethttpomithttp2 -mod=vendor -o ./ipp-usb .
export IPP_USB_BRIDGE_BIN="$SCRIPT_DIR/ipp-usb"

FILTER="TestBridge"

case "${1:-all}" in
    all)
        FILTER="TestBridge"
        ;;
    discovery)
        FILTER="TestBridgeDiscovery"
        ;;
    lifecycle)
        FILTER="TestBridge(Ready|Shutdown|GracefulDrain)"
        ;;
    print)
        FILTER="TestBridge(Print|Cancel|GetPrinter|GetJobs)"
        ;;
    *)
        echo "Unknown target: $1 (use: all, discovery, lifecycle, print)" >&2
        exit 1
        ;;
esac

echo "Running: go test -v -tags \"$TAGS\" -mod=vendor -run \"$FILTER\" -count=1"
echo ""

go test -v -tags "$TAGS" -mod=vendor -run "$FILTER" -count=1
