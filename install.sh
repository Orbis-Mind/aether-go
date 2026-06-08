#!/bin/sh
# Aether Installer for Mac and Linux
# Run with: curl -fsSL https://raw.githubusercontent.com/Orbis-Mind/aether/main/install.sh | sh

set -e

REPO="Orbis-Mind/aether"
INSTALL_DIR="/usr/local/bin"
BINARY_NAME="aether"

echo ""
echo "  Aether AI Compiler and Runtime — Installer"
echo "  ==========================================="
echo ""

# Detect OS and architecture
OS="$(uname -s)"
ARCH="$(uname -m)"

case "$OS" in
    Darwin)
        case "$ARCH" in
            arm64) ASSET="aether-mac-arm" ;;
            x86_64) ASSET="aether-mac-intel" ;;
            *) echo "  Unsupported Mac architecture: $ARCH"; exit 1 ;;
        esac
        ;;
    Linux)
        case "$ARCH" in
            x86_64) ASSET="aether-linux" ;;
            *) echo "  Unsupported Linux architecture: $ARCH"; exit 1 ;;
        esac
        ;;
    *)
        echo "  Unsupported OS: $OS"
        echo "  For Windows, use install.ps1 instead"
        exit 1
        ;;
esac

echo "  OS      : $OS ($ARCH)"
echo "  Asset   : $ASSET"
echo "  Install : $INSTALL_DIR/$BINARY_NAME"
echo ""

# Get latest release version
echo "  Fetching latest version..."
VERSION=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" | grep '"tag_name"' | sed 's/.*"tag_name": "\(.*\)".*/\1/')
echo "  Version : $VERSION"
echo ""

# Download URL
URL="https://github.com/$REPO/releases/download/$VERSION/$ASSET"

echo "  Downloading..."
curl -fsSL "$URL" -o "/tmp/$BINARY_NAME"
chmod +x "/tmp/$BINARY_NAME"

# Install
echo "  Installing to $INSTALL_DIR..."
if [ -w "$INSTALL_DIR" ]; then
    mv "/tmp/$BINARY_NAME" "$INSTALL_DIR/$BINARY_NAME"
else
    sudo mv "/tmp/$BINARY_NAME" "$INSTALL_DIR/$BINARY_NAME"
fi

echo ""
echo "  Installation complete!"
echo ""
echo "  Run:"
echo "    aether --help"
echo "    aether init my-project"
echo "    aether build"
echo "    aether run --mock --message 'Hello'"
echo ""
