#!/bin/bash
# build-bridge.sh — Cross-compile ipp-usb bridge for all platforms.
#
# Builds the ipp-usb bridge binary for macOS (arm64, amd64), Linux (amd64, arm64),
# and Windows (amd64). Uses zig as the C cross-compiler for non-native targets.
#
# Prerequisites:
#   - Go 1.19+
#   - Zig: brew install zig
#   - p7zip (for Windows libusb): brew install p7zip
#
# Usage:
#   ./build-bridge.sh              # Build all targets
#   ./build-bridge.sh clean        # Remove build artifacts
#   ./build-bridge.sh darwin       # Build macOS targets only
#   ./build-bridge.sh linux        # Build Linux targets only
#   ./build-bridge.sh windows      # Build Windows target only

set -e

LIBUSB_VERSION="1.0.27"
LIBUSB_URL="https://github.com/libusb/libusb/releases/download/v${LIBUSB_VERSION}/libusb-${LIBUSB_VERSION}.tar.bz2"
LIBUSB_WIN_URL="https://github.com/libusb/libusb/releases/download/v${LIBUSB_VERSION}/libusb-${LIBUSB_VERSION}.7z"

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
BUILD_DIR="$SCRIPT_DIR/.build-bridge"
DIST_DIR="$SCRIPT_DIR/dist"
LIBUSB_SRC="$BUILD_DIR/libusb-${LIBUSB_VERSION}"

VERSION=$(git describe --tags --always 2>/dev/null || echo "dev")
LDFLAGS="-s -w -X main.Version=$VERSION"
TAGS="nethttpomithttp2,noavahi"

# --- Helpers ---

die() { echo "Error: $*" >&2; exit 1; }

check_prereqs() {
    command -v go >/dev/null 2>&1 || die "go not found"
    command -v zig >/dev/null 2>&1 || die "zig not found (brew install zig)"
}

# Download and extract libusb source for headers and cross-compilation.
setup_libusb_source() {
    if [ -d "$LIBUSB_SRC" ]; then
        echo "Using cached libusb source: $LIBUSB_SRC"
        return
    fi
    mkdir -p "$BUILD_DIR"
    echo "Downloading libusb ${LIBUSB_VERSION} source..."
    curl -fSL -o "$BUILD_DIR/libusb.tar.bz2" "$LIBUSB_URL"
    tar xjf "$BUILD_DIR/libusb.tar.bz2" -C "$BUILD_DIR"
}

# Build libusb as a static archive for a given target.
#   $1: target name (e.g., linux-amd64)
#   $2: configure --host
#   $3: CC command
build_libusb_static() {
    local TARGET="$1" HOST="$2" CC_CMD="$3"
    local TARGET_DIR="$BUILD_DIR/libusb-$TARGET"

    if [ -f "$TARGET_DIR/libusb/.libs/libusb-1.0.a" ]; then
        echo "  Using cached libusb for $TARGET"
        return
    fi

    echo "  Building libusb static for $TARGET..."
    rm -rf "$TARGET_DIR"
    mkdir -p "$TARGET_DIR"

    # For cross-compilation targets, use llvm-ar (via zig) to create
    # archives in GNU format that lld can link. macOS native ar creates
    # BSD-format archives which may not be compatible with cross-linkers.
    local AR_CMD="ar"
    local RANLIB_CMD="ranlib"
    case "$HOST" in
        *linux*|*windows*)
            # Create zig ar/ranlib wrapper scripts
            AR_CMD="$BUILD_DIR/zig-ar"
            RANLIB_CMD="$BUILD_DIR/zig-ranlib"
            cat > "$AR_CMD" <<'EOF'
#!/bin/sh
exec zig ar "$@"
EOF
            cat > "$RANLIB_CMD" <<'EOF'
#!/bin/sh
exec zig ranlib "$@"
EOF
            chmod +x "$AR_CMD" "$RANLIB_CMD"
            ;;
    esac

    (
        cd "$TARGET_DIR"
        "$LIBUSB_SRC/configure" \
            --host="$HOST" \
            --enable-static \
            --disable-shared \
            --disable-examples-build \
            --disable-tests-build \
            --disable-udev \
            --with-pic \
            CC="$CC_CMD" \
            AR="$AR_CMD" \
            RANLIB="$RANLIB_CMD" \
            CFLAGS="-fPIC" \
            >/dev/null 2>&1
        make -j"$(sysctl -n hw.ncpu 2>/dev/null || nproc 2>/dev/null || echo 4)" \
            >/dev/null 2>&1
    )
}

# Create zig wrapper script for a target.
#   $1: zig target triple
#   Returns: path to wrapper in ZIG_CC variable
make_zig_cc() {
    local ZIG_TARGET="$1"
    ZIG_CC="$BUILD_DIR/zig-cc-${ZIG_TARGET}"
    cat > "$ZIG_CC" <<EOF
#!/bin/sh
exec zig cc -target $ZIG_TARGET "\$@"
EOF
    chmod +x "$ZIG_CC"
}

# Build the bridge binary for one target.
#   $1: GOOS
#   $2: GOARCH
#   $3: output platform dir name (e.g., darwin-aarch64)
#   $4: CC command (or "native" to use system cc)
#   $5: CGO_CFLAGS
#   $6: CGO_LDFLAGS
build_bridge() {
    local GOOS_VAL="$1" GOARCH_VAL="$2" PLATFORM="$3" CC_VAL="$4"
    local CFLAGS_VAL="$5" LDFLAGS_VAL="$6"
    local OUTPUT="$DIST_DIR/$PLATFORM/ipp-usb-bridge"
    [ "$GOOS_VAL" = "windows" ] && OUTPUT="${OUTPUT}.exe"

    echo ""
    echo "=== Building $PLATFORM ==="
    echo "  GOOS=$GOOS_VAL GOARCH=$GOARCH_VAL"
    echo "  CC=$CC_VAL"

    mkdir -p "$DIST_DIR/$PLATFORM"

    # For Linux (musl): add -extldflags "-static" for fully static binary
    local BUILD_LDFLAGS="$LDFLAGS"
    if [ "$GOOS_VAL" = "linux" ]; then
        BUILD_LDFLAGS="$LDFLAGS -extldflags \"-static\""
    fi

    # Export environment for go build. Using explicit export/unset avoids
    # shell parsing issues with inline VAR=value and conditional expansions.
    export CGO_ENABLED=1
    export GOOS="$GOOS_VAL"
    export GOARCH="$GOARCH_VAL"
    export CGO_CFLAGS="$CFLAGS_VAL"
    export CGO_LDFLAGS="$LDFLAGS_VAL"
    if [ "$CC_VAL" != "native" ]; then
        export CC="$CC_VAL"
    else
        unset CC
    fi

    go build \
        -ldflags "$BUILD_LDFLAGS" \
        -tags "$TAGS" \
        -mod=vendor \
        -o "$OUTPUT" \
        .

    # Clean up exported variables
    unset CGO_ENABLED GOOS GOARCH CGO_CFLAGS CGO_LDFLAGS CC

    echo "  -> $OUTPUT ($(du -h "$OUTPUT" | awk '{print $1}'))"
    file "$OUTPUT"
}

# --- Platform Builders ---

build_darwin_arm64() {
    echo ""
    echo "=== darwin-aarch64 (native) ==="
    setup_libusb_source
    build_libusb_static "darwin-arm64" "aarch64-apple-darwin" "cc"

    local LIBUSB_DIR="$BUILD_DIR/libusb-darwin-arm64"
    build_bridge "darwin" "arm64" "darwin-aarch64" "native" \
        "-I$LIBUSB_SRC/libusb" \
        "-L$LIBUSB_DIR/libusb/.libs -lusb-1.0 -framework IOKit -framework CoreFoundation -framework Security"
}

build_darwin_amd64() {
    setup_libusb_source
    build_libusb_static "darwin-amd64" "x86_64-apple-darwin" "cc -arch x86_64"

    local LIBUSB_DIR="$BUILD_DIR/libusb-darwin-amd64"
    build_bridge "darwin" "amd64" "darwin-x86-64" "cc -arch x86_64" \
        "-I$LIBUSB_SRC/libusb -arch x86_64" \
        "-L$LIBUSB_DIR/libusb/.libs -lusb-1.0 -framework IOKit -framework CoreFoundation -framework Security -arch x86_64"
}

build_linux_amd64() {
    setup_libusb_source
    make_zig_cc "x86_64-linux-musl"
    build_libusb_static "linux-amd64" "x86_64-linux-musl" "$ZIG_CC"

    local LIBUSB_DIR="$BUILD_DIR/libusb-linux-amd64"
    build_bridge "linux" "amd64" "linux-x86-64" "$ZIG_CC" \
        "-I$LIBUSB_SRC/libusb" \
        "-L$LIBUSB_DIR/libusb/.libs -lusb-1.0 -lpthread -static"
}

build_linux_arm64() {
    setup_libusb_source
    make_zig_cc "aarch64-linux-musl"
    build_libusb_static "linux-arm64" "aarch64-linux-musl" "$ZIG_CC"

    local LIBUSB_DIR="$BUILD_DIR/libusb-linux-arm64"
    build_bridge "linux" "arm64" "linux-aarch64" "$ZIG_CC" \
        "-I$LIBUSB_SRC/libusb" \
        "-L$LIBUSB_DIR/libusb/.libs -lusb-1.0 -lpthread -static"
}

build_windows_amd64() {
    setup_libusb_source

    # Download pre-built Windows libusb (headers + static lib)
    local WIN_7Z="$BUILD_DIR/libusb-win.7z"
    local WIN_DIR="$BUILD_DIR/libusb-windows"
    if [ ! -d "$WIN_DIR" ]; then
        if ! command -v 7z >/dev/null 2>&1; then
            die "7z not found (brew install p7zip) — needed for Windows libusb"
        fi
        echo "  Downloading Windows libusb..."
        [ -f "$WIN_7Z" ] || curl -fSL -o "$WIN_7Z" "$LIBUSB_WIN_URL"
        rm -rf "$WIN_DIR"
        mkdir -p "$WIN_DIR"
        7z x -o"$WIN_DIR" "$WIN_7Z" >/dev/null
    fi

    # Find MinGW64 static lib and headers.
    # libusb archive layout: include/libusb.h, MinGW64/static/libusb-1.0.a
    local INCLUDE_DIR
    INCLUDE_DIR=$(find "$WIN_DIR" -name "libusb.h" -not -path "*/examples/*" -exec dirname {} \; | head -1)
    local LIB_DIR
    LIB_DIR=$(find "$WIN_DIR" -path "*/MinGW64/static" -type d | head -1)

    [ -n "$INCLUDE_DIR" ] || die "Windows libusb headers not found in archive"
    [ -d "$LIB_DIR" ] || die "Windows libusb static lib not found in archive"

    make_zig_cc "x86_64-windows-gnu"
    # Static link libusb; -lsetupapi -lole32 are Windows system libs needed by libusb
    build_bridge "windows" "amd64" "win32-x86-64" "$ZIG_CC" \
        "-I$INCLUDE_DIR" \
        "-L$LIB_DIR -lusb-1.0 -lsetupapi -lole32 -static"
}

# --- Quirks ---

copy_quirks() {
    echo ""
    echo "=== Copying quirks database ==="
    rm -rf "$DIST_DIR/ipp-usb-quirks"
    cp -r "$SCRIPT_DIR/ipp-usb-quirks" "$DIST_DIR/ipp-usb-quirks"
    echo "  -> dist/ipp-usb-quirks/ ($(ls "$DIST_DIR/ipp-usb-quirks" | wc -l | tr -d ' ') files)"
}

# --- Main ---

if [ "${1:-}" = "clean" ]; then
    echo "Cleaning..."
    rm -rf "$BUILD_DIR" "$DIST_DIR"
    echo "Done."
    exit 0
fi

check_prereqs

TARGET="${1:-all}"

case "$TARGET" in
    all)
        build_darwin_arm64
        build_darwin_amd64
        build_linux_amd64
        build_linux_arm64
        build_windows_amd64
        ;;
    darwin)
        build_darwin_arm64
        build_darwin_amd64
        ;;
    linux)
        build_linux_amd64
        build_linux_arm64
        ;;
    windows)
        build_windows_amd64
        ;;
    *)
        die "Unknown target: $TARGET (use: all, darwin, linux, windows, clean)"
        ;;
esac

copy_quirks

echo ""
echo "=== Summary ==="
for plat in darwin-aarch64 darwin-x86-64 linux-x86-64 linux-aarch64 win32-x86-64; do
    if [ -f "$DIST_DIR/$plat/ipp-usb-bridge" ] || [ -f "$DIST_DIR/$plat/ipp-usb-bridge.exe" ]; then
        printf "  %-16s %s\n" "$plat" "$(du -h "$DIST_DIR/$plat"/ipp-usb-bridge* | awk '{print $1}')"
    else
        printf "  %-16s (not built)\n" "$plat"
    fi
done
echo ""
echo "Done. Binaries in: $DIST_DIR/"
echo "Run './build-bridge.sh clean' to remove build artifacts."
