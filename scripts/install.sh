#!/usr/bin/env bash
# Watchfire installer for macOS and Linux
# Usage: curl -fsSL https://raw.githubusercontent.com/watchfire-io/watchfire/main/scripts/install.sh | bash
set -euo pipefail

REPO="watchfire-io/watchfire"
INSTALL_DIR="${WATCHFIRE_INSTALL_DIR:-}"
TMP_DIR=""

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

info()  { printf "${CYAN}▸${NC} %s\n" "$1"; }
ok()    { printf "${GREEN}✓${NC} %s\n" "$1"; }
warn()  { printf "${YELLOW}!${NC} %s\n" "$1"; }
error() { printf "${RED}✗${NC} %s\n" "$1" >&2; exit 1; }

# Detect OS
detect_os() {
    case "$(uname -s)" in
        Darwin) echo "darwin" ;;
        Linux)  echo "linux" ;;
        *)      error "Unsupported OS: $(uname -s). Use the PowerShell script for Windows." ;;
    esac
}

# Detect architecture
detect_arch() {
    case "$(uname -m)" in
        x86_64|amd64)   echo "amd64" ;;
        arm64|aarch64)  echo "arm64" ;;
        *)              error "Unsupported architecture: $(uname -m)" ;;
    esac
}

# Determine install directory
determine_install_dir() {
    if [ -n "$INSTALL_DIR" ]; then
        echo "$INSTALL_DIR"
        return
    fi

    # Prefer /usr/local/bin if writable, otherwise ~/.local/bin
    if [ -w "/usr/local/bin" ]; then
        echo "/usr/local/bin"
    else
        echo "${HOME}/.local/bin"
    fi
}

# Fetch latest version from GitHub Releases
fetch_latest_version() {
    local url="https://api.github.com/repos/${REPO}/releases/latest"
    local version

    if command -v curl >/dev/null 2>&1; then
        version=$(curl -fsSL "$url" | grep '"tag_name"' | sed -E 's/.*"v([^"]+)".*/\1/')
    elif command -v wget >/dev/null 2>&1; then
        version=$(wget -qO- "$url" | grep '"tag_name"' | sed -E 's/.*"v([^"]+)".*/\1/')
    else
        error "curl or wget is required"
    fi

    if [ -z "$version" ]; then
        error "Failed to fetch latest version from GitHub"
    fi
    echo "$version"
}

# Download a file
download() {
    local url="$1" dest="$2"
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL -o "$dest" "$url"
    elif command -v wget >/dev/null 2>&1; then
        wget -qO "$dest" "$url"
    fi
}

main() {
    printf "\n${CYAN}Watchfire Installer${NC}\n\n"

    local os arch version install_dir
    os=$(detect_os)
    arch=$(detect_arch)
    version=$(fetch_latest_version)
    install_dir=$(determine_install_dir)

    info "OS: $os | Arch: $arch | Version: v$version"
    info "Install directory: $install_dir"

    # Create install dir if needed
    mkdir -p "$install_dir"

    local base_url="https://github.com/${REPO}/releases/download/v${version}"
    local cli_name="watchfire-${os}-${arch}"
    local daemon_name="watchfired-${os}-${arch}"

    # Download binaries
    TMP_DIR=$(mktemp -d)
    trap 'rm -rf "$TMP_DIR"' EXIT

    info "Downloading watchfire v${version}..."
    download "${base_url}/${cli_name}" "${TMP_DIR}/watchfire"
    download "${base_url}/${daemon_name}" "${TMP_DIR}/watchfired"

    chmod +x "${TMP_DIR}/watchfire" "${TMP_DIR}/watchfired"

    # Install
    info "Installing to ${install_dir}..."
    if [ -w "$install_dir" ]; then
        mv "${TMP_DIR}/watchfire" "${install_dir}/watchfire"
        mv "${TMP_DIR}/watchfired" "${install_dir}/watchfired"
    else
        warn "Elevated permissions required for ${install_dir}"
        sudo mv "${TMP_DIR}/watchfire" "${install_dir}/watchfire"
        sudo mv "${TMP_DIR}/watchfired" "${install_dir}/watchfired"
    fi

    # macOS: remove quarantine and ad-hoc sign
    if [ "$os" = "darwin" ]; then
        xattr -dr com.apple.quarantine "${install_dir}/watchfire" 2>/dev/null || true
        xattr -dr com.apple.quarantine "${install_dir}/watchfired" 2>/dev/null || true
        codesign --sign - --force "${install_dir}/watchfire" 2>/dev/null || true
        codesign --sign - --force "${install_dir}/watchfired" 2>/dev/null || true
    fi

    # Verify
    if "${install_dir}/watchfire" version >/dev/null 2>&1; then
        ok "watchfire v${version} installed successfully!"
    else
        error "Installation failed — binary not executable"
    fi

    # Check if install dir is in PATH
    if ! echo "$PATH" | tr ':' '\n' | grep -qx "$install_dir"; then
        printf "\n"
        warn "${install_dir} is not in your PATH."
        echo "  Add it by running:"
        echo ""
        if [ -f "${HOME}/.zshrc" ]; then
            echo "    echo 'export PATH=\"${install_dir}:\$PATH\"' >> ~/.zshrc && source ~/.zshrc"
        else
            echo "    echo 'export PATH=\"${install_dir}:\$PATH\"' >> ~/.bashrc && source ~/.bashrc"
        fi
        echo ""
    fi

    printf "\n${GREEN}Get started:${NC}\n"
    echo "  cd your-project"
    echo "  watchfire init"
    echo "  watchfire run"
    printf "\n"
}

main "$@"
