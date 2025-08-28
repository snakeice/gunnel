#!/bin/sh
set -e

# Gunnel installation script
# Usage: curl -sSL https://raw.githubusercontent.com/snakeice/gunnel/main/install.sh | sh

REPO="snakeice/gunnel"
BINARY="gunnel"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"

# Detect OS and architecture
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

case "$ARCH" in
    x86_64|amd64)
        ARCH="x86_64"
        ;;
    aarch64|arm64)
        ARCH="arm64"
        ;;
    armv7l|armv7)
        ARCH="armv7"
        ;;
    i386|i686)
        ARCH="i386"
        ;;
    *)
        echo "Unsupported architecture: $ARCH"
        exit 1
        ;;
esac

case "$OS" in
    linux)
        OS="Linux"
        ;;
    darwin)
        OS="Darwin"
        ;;
    freebsd)
        OS="FreeBSD"
        ;;
    *)
        echo "Unsupported OS: $OS"
        exit 1
        ;;
esac

# Get latest release
echo "Fetching latest release..."
LATEST_RELEASE=$(curl -s "https://api.github.com/repos/$REPO/releases/latest" | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')

if [ -z "$LATEST_RELEASE" ]; then
    echo "Failed to get latest release"
    exit 1
fi

echo "Latest release: $LATEST_RELEASE"

# Construct download URL
FILENAME="${BINARY}_${OS}_${ARCH}"
if [ "$OS" = "Windows" ]; then
    FILENAME="${FILENAME}.zip"
else
    FILENAME="${FILENAME}.tar.gz"
fi

DOWNLOAD_URL="https://github.com/$REPO/releases/download/$LATEST_RELEASE/$FILENAME"

# Create temp directory
TEMP_DIR=$(mktemp -d)
trap "rm -rf $TEMP_DIR" EXIT

# Download and extract
echo "Downloading $DOWNLOAD_URL..."
cd "$TEMP_DIR"

if command -v curl >/dev/null 2>&1; then
    curl -L -o "$FILENAME" "$DOWNLOAD_URL"
elif command -v wget >/dev/null 2>&1; then
    wget -O "$FILENAME" "$DOWNLOAD_URL"
else
    echo "Neither curl nor wget found. Please install one of them."
    exit 1
fi

echo "Extracting..."
if [ "$OS" = "Windows" ]; then
    unzip "$FILENAME"
else
    tar -xzf "$FILENAME"
fi

# Install binary
echo "Installing to $INSTALL_DIR..."
if [ -w "$INSTALL_DIR" ]; then
    mv "$BINARY" "$INSTALL_DIR/"
    chmod +x "$INSTALL_DIR/$BINARY"
else
    echo "Need sudo privileges to install to $INSTALL_DIR"
    sudo mv "$BINARY" "$INSTALL_DIR/"
    sudo chmod +x "$INSTALL_DIR/$BINARY"
fi

# Verify installation
if command -v "$BINARY" >/dev/null 2>&1; then
    echo ""
    echo "✅ Gunnel installed successfully!"
    echo ""
    "$BINARY" --version
    echo ""
    echo "To get started:"
    echo "  gunnel --help"
    echo ""
    echo "Example configs can be found at:"
    echo "  https://github.com/$REPO/tree/main/example"
else
    echo "❌ Installation failed"
    exit 1
fi
