-include .env
export POSTHOG_KEY

# ============================================================
# Watchfire — Makefile
# ============================================================
# Main entrypoints:
#
#  Development
#    make dev-daemon            — daemon with hot reload (air)
#    make dev-tui               — build + run TUI
#    make dev-gui               — Electron dev mode
#
#  macOS
#    make install-darwin        — build + install binaries to /usr/local/bin
#    make install-gui           — build + install Watchfire.app to /Applications
#    make package-darwin        — universal release package (Go binaries + Electron app)
#
#  Linux
#    make install-linux         — install runtime deps, build, copy to /usr/local/bin
#    make package-linux         — per-arch release tarballs with install script
#
#  Common
#    make build                 — build native binaries (current OS/arch)
#    make test                  — run tests
#    make lint                  — run linter
#    make proto                 — regenerate protobuf code
#    make clean                 — remove build artifacts
#    make install-tools         — install dev tools (air, golangci-lint, protoc plugins)
# ============================================================

.PHONY: help dev-daemon dev-tui dev-gui gui-deps \
        build build-daemon build-cli \
        build-darwin build-daemon-arm64 build-daemon-amd64 build-cli-arm64 build-cli-amd64 \
        build-linux build-linux-amd64 build-linux-arm64 build-gui \
        install-darwin install-linux install-gui \
        install-linux-runtime-deps install-linux-build-deps \
        package-darwin package-linux \
        sync-version proto test lint clean install-tools tidy uninstall

# ── Variables ────────────────────────────────────────────────

DAEMON_BINARY := watchfired
CLI_BINARY    := watchfire
BUILD_DIR     := build
CMD_DIR       := cmd

GOCMD   := go
GOBUILD := $(GOCMD) build
GOTEST  := $(GOCMD) test
GOMOD   := $(GOCMD) mod

VERSION    := $(shell python3 -c "import json; print(json.load(open('version.json'))['version'])")
CODENAME   := $(shell python3 -c "import json; print(json.load(open('version.json'))['codename'])")
COMMIT     := $(shell git rev-parse --short HEAD)
BUILD_DATE := $(shell date -u +%Y-%m-%d)

LDFLAGS := -X github.com/watchfire-io/watchfire/internal/buildinfo.Version=$(VERSION) \
           -X github.com/watchfire-io/watchfire/internal/buildinfo.Codename=$(CODENAME) \
           -X github.com/watchfire-io/watchfire/internal/buildinfo.CommitHash=$(COMMIT) \
           -X github.com/watchfire-io/watchfire/internal/buildinfo.BuildDate=$(BUILD_DATE) \
           -X github.com/watchfire-io/watchfire/internal/buildinfo.PostHogKey=$(POSTHOG_KEY)

NATIVE_ARCH := $(shell uname -m | sed 's/x86_64/x64/')

# On Linux, detect which appindicator pkg-config name is available.
# Debian/Ubuntu ship ayatana-appindicator3-0.1; Fedora/RHEL ship appindicator3-0.1 (legacy).
ifeq ($(shell uname -s),Linux)
  ifeq ($(shell pkg-config --exists ayatana-appindicator3-0.1 2>/dev/null && echo yes),yes)
    LINUX_BUILD_TAGS :=
  else
    LINUX_BUILD_TAGS := -tags legacy_appindicator
  endif
else
  LINUX_BUILD_TAGS :=
endif

# ── Default ──────────────────────────────────────────────────

help:
	@echo ""
	@echo "  Development"
	@echo "    make dev-daemon            — daemon with hot reload (air)"
	@echo "    make dev-tui               — build + run TUI"
	@echo "    make dev-gui               — Electron dev mode"
	@echo ""
	@echo "  macOS"
	@echo "    make install-darwin        — build + install binaries to /usr/local/bin"
	@echo "    make install-gui           — build + install Watchfire.app to /Applications"
	@echo "    make package-darwin        — universal release package"
	@echo ""
	@echo "  Linux"
	@echo "    make install-linux         — install runtime deps, build, copy to /usr/local/bin"
	@echo "    make package-linux         — per-arch release tarballs"
	@echo ""
	@echo "  Common"
	@echo "    make build                 — build native binaries (current OS/arch)"
	@echo "    make test                  — run tests"
	@echo "    make lint                  — run linter"
	@echo "    make clean                 — remove build artifacts"
	@echo "    make install-tools         — install dev tools"
	@echo ""

# ── Development ──────────────────────────────────────────────

dev-daemon:
	@echo "Starting daemon with hot reload..."
	air -c .air.toml

dev-tui: build-cli
	./$(BUILD_DIR)/$(CLI_BINARY)

dev-gui: gui-deps
	cd gui && npm run dev

# Install GUI npm deps and rebuild native modules (node-pty) against Electron
gui-deps:
	cd gui && npm install
	cd gui && npx electron-rebuild -f -w node-pty

# ── Build (native) ───────────────────────────────────────────

build: build-daemon build-cli

build-daemon:
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=1 $(GOBUILD) $(LINUX_BUILD_TAGS) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(DAEMON_BINARY) ./$(CMD_DIR)/watchfired

build-cli:
	@mkdir -p $(BUILD_DIR)
	$(GOBUILD) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(CLI_BINARY) ./$(CMD_DIR)/watchfire

# ── macOS ────────────────────────────────────────────────────

# Build universal (arm64 + amd64) fat binaries via lipo
build-darwin: build-daemon-arm64 build-daemon-amd64 build-cli-arm64 build-cli-amd64
	@echo "Creating universal binaries..."
	lipo -create -output $(BUILD_DIR)/$(DAEMON_BINARY) $(BUILD_DIR)/$(DAEMON_BINARY)-arm64 $(BUILD_DIR)/$(DAEMON_BINARY)-amd64
	lipo -create -output $(BUILD_DIR)/$(CLI_BINARY) $(BUILD_DIR)/$(CLI_BINARY)-arm64 $(BUILD_DIR)/$(CLI_BINARY)-amd64
	lipo -info $(BUILD_DIR)/$(DAEMON_BINARY)
	lipo -info $(BUILD_DIR)/$(CLI_BINARY)
	@# x64 aliases for electron-builder ${arch} substitution
	cp $(BUILD_DIR)/$(DAEMON_BINARY)-amd64 $(BUILD_DIR)/$(DAEMON_BINARY)-x64
	cp $(BUILD_DIR)/$(CLI_BINARY)-amd64    $(BUILD_DIR)/$(CLI_BINARY)-x64

build-daemon-arm64:
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=1 GOARCH=arm64 $(GOBUILD) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(DAEMON_BINARY)-arm64 ./$(CMD_DIR)/watchfired

build-daemon-amd64:
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=1 GOARCH=amd64 $(GOBUILD) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(DAEMON_BINARY)-amd64 ./$(CMD_DIR)/watchfired

build-cli-arm64:
	@mkdir -p $(BUILD_DIR)
	GOARCH=arm64 $(GOBUILD) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(CLI_BINARY)-arm64 ./$(CMD_DIR)/watchfire

build-cli-amd64:
	@mkdir -p $(BUILD_DIR)
	GOARCH=amd64 $(GOBUILD) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(CLI_BINARY)-amd64 ./$(CMD_DIR)/watchfire

# Build native binaries and install to /usr/local/bin with codesign
install-darwin: build
	@if pgrep -x watchfired >/dev/null 2>&1; then \
		echo "Stopping running daemon..."; \
		pkill -TERM watchfired 2>/dev/null; \
		sleep 1; \
	fi
	@echo "Installing watchfire v$(VERSION) to /usr/local/bin..."
	@cp $(BUILD_DIR)/$(CLI_BINARY) /usr/local/bin/$(CLI_BINARY) 2>/dev/null || \
		sudo cp $(BUILD_DIR)/$(CLI_BINARY) /usr/local/bin/$(CLI_BINARY)
	@cp $(BUILD_DIR)/$(DAEMON_BINARY) /usr/local/bin/$(DAEMON_BINARY) 2>/dev/null || \
		sudo cp $(BUILD_DIR)/$(DAEMON_BINARY) /usr/local/bin/$(DAEMON_BINARY)
	@xattr -dr com.apple.quarantine /usr/local/bin/$(CLI_BINARY) 2>/dev/null || \
		sudo xattr -dr com.apple.quarantine /usr/local/bin/$(CLI_BINARY) 2>/dev/null || true
	@xattr -dr com.apple.quarantine /usr/local/bin/$(DAEMON_BINARY) 2>/dev/null || \
		sudo xattr -dr com.apple.quarantine /usr/local/bin/$(DAEMON_BINARY) 2>/dev/null || true
	@codesign --sign - --force /usr/local/bin/$(CLI_BINARY) 2>/dev/null || \
		sudo codesign --sign - --force /usr/local/bin/$(CLI_BINARY)
	@codesign --sign - --force /usr/local/bin/$(DAEMON_BINARY) 2>/dev/null || \
		sudo codesign --sign - --force /usr/local/bin/$(DAEMON_BINARY)
	@echo "Installed:"; $(BUILD_DIR)/$(CLI_BINARY) version; $(BUILD_DIR)/$(DAEMON_BINARY) version

# Build and install Watchfire.app to /Applications (native arch only)
install-gui: sync-version build
	@cp $(BUILD_DIR)/$(DAEMON_BINARY) $(BUILD_DIR)/$(DAEMON_BINARY)-$(NATIVE_ARCH)
	@cp $(BUILD_DIR)/$(CLI_BINARY)    $(BUILD_DIR)/$(CLI_BINARY)-$(NATIVE_ARCH)
	@sed 's/arch: \[universal\]/arch: [$(NATIVE_ARCH)]/' gui/electron-builder.yml > gui/electron-builder.local.yml
	@echo "Packaging Watchfire.app ($(NATIVE_ARCH)) for local install..."
	cd gui && npx electron-builder --config electron-builder.local.yml --publish never --mac -c.mac.notarize=false
	@rm -f gui/electron-builder.local.yml
	@echo "Installing Watchfire.app to /Applications..."
	@APP_DIR=$$(ls -d gui/dist/mac-$(NATIVE_ARCH)/Watchfire.app 2>/dev/null | head -1); \
	if [ -z "$$APP_DIR" ]; then echo "Error: Watchfire.app not found in gui/dist/"; exit 1; fi; \
	rm -rf /Applications/Watchfire.app 2>/dev/null || sudo rm -rf /Applications/Watchfire.app; \
	cp -R "$$APP_DIR" /Applications/Watchfire.app 2>/dev/null || \
		sudo cp -R "$$APP_DIR" /Applications/Watchfire.app
	@xattr -dr com.apple.quarantine /Applications/Watchfire.app 2>/dev/null || true
	@echo "Watchfire.app installed to /Applications"

# Full macOS release package — universal Go binaries + packaged Electron app
package-darwin: sync-version build-darwin build-gui
	cd gui && npm run package

build-gui: gui-deps
	cd gui && npm run build

# ── Linux ────────────────────────────────────────────────────

# Install runtime deps (bubblewrap for sandboxing, libayatana-appindicator for tray)
install-linux-runtime-deps:
	@if command -v apt-get >/dev/null 2>&1; then \
		sudo apt-get update -qq && \
		sudo apt-get install -y --no-install-recommends bubblewrap libayatana-appindicator3-1; \
	elif command -v dnf >/dev/null 2>&1; then \
		sudo dnf install -y bubblewrap; \
		sudo dnf install -y libayatana-appindicator 2>/dev/null || \
			sudo dnf install -y libappindicator-gtk3 2>/dev/null || \
			echo "Warning: appindicator not found — system tray may be unavailable"; \
	elif command -v pacman >/dev/null 2>&1; then \
		sudo pacman -Sy --noconfirm bubblewrap libayatana-appindicator; \
	elif command -v zypper >/dev/null 2>&1; then \
		sudo zypper install -y bubblewrap libayatana-appindicator3-1; \
	else \
		echo "Unknown package manager — install manually: bubblewrap and libayatana-appindicator3"; exit 1; \
	fi

# Install build-time headers required for CGO (system tray compilation)
install-linux-build-deps:
	@if command -v apt-get >/dev/null 2>&1; then \
		sudo apt-get install -y libayatana-appindicator3-dev; \
	elif command -v dnf >/dev/null 2>&1; then \
		sudo dnf install -y libayatana-appindicator-devel || sudo dnf install -y libappindicator-gtk3-devel; \
	elif command -v pacman >/dev/null 2>&1; then \
		sudo pacman -Sy --noconfirm libayatana-appindicator; \
	elif command -v zypper >/dev/null 2>&1; then \
		sudo zypper install -y libayatana-appindicator3-devel; \
	else \
		echo "Unknown package manager — install libayatana-appindicator3-dev manually"; exit 1; \
	fi

# Install runtime deps, build native binaries, and copy to /usr/local/bin
install-linux: install-linux-runtime-deps install-linux-build-deps build
	@if pgrep -x watchfired >/dev/null 2>&1; then \
		echo "Stopping running daemon..."; \
		pkill -TERM watchfired 2>/dev/null; \
		sleep 1; \
	fi
	@echo "Installing watchfire v$(VERSION) to /usr/local/bin..."
	sudo install -m 755 $(BUILD_DIR)/$(CLI_BINARY)    /usr/local/bin/$(CLI_BINARY)
	sudo install -m 755 $(BUILD_DIR)/$(DAEMON_BINARY) /usr/local/bin/$(DAEMON_BINARY)
	@echo "Installed:"; $(BUILD_DIR)/$(CLI_BINARY) version; $(BUILD_DIR)/$(DAEMON_BINARY) version

# Build Linux binaries for both architectures (run on a Linux host or with cross-compile toolchain)
# Install build-time headers first: make install-linux-build-deps
build-linux: build-linux-amd64 build-linux-arm64

build-linux-amd64:
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=1 GOOS=linux GOARCH=amd64 $(GOBUILD) $(LINUX_BUILD_TAGS) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(DAEMON_BINARY)-linux-amd64 ./$(CMD_DIR)/watchfired
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GOBUILD) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(CLI_BINARY)-linux-amd64   ./$(CMD_DIR)/watchfire

build-linux-arm64:
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=1 GOOS=linux GOARCH=arm64 $(GOBUILD) $(LINUX_BUILD_TAGS) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(DAEMON_BINARY)-linux-arm64 ./$(CMD_DIR)/watchfired
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 $(GOBUILD) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(CLI_BINARY)-linux-arm64   ./$(CMD_DIR)/watchfire

# Package Linux binaries into per-arch tarballs with install script
package-linux: build-linux
	@echo "Creating Linux packages..."
	@for arch in amd64 arm64; do \
		dir=$(BUILD_DIR)/watchfire-linux-$$arch; \
		mkdir -p $$dir; \
		cp $(BUILD_DIR)/$(DAEMON_BINARY)-linux-$$arch $$dir/$(DAEMON_BINARY); \
		cp $(BUILD_DIR)/$(CLI_BINARY)-linux-$$arch    $$dir/$(CLI_BINARY); \
		cp scripts/install-linux.sh $$dir/install.sh; \
		chmod +x $$dir/install.sh; \
		tar -czf $(BUILD_DIR)/watchfire-linux-$$arch.tar.gz -C $(BUILD_DIR) watchfire-linux-$$arch; \
		rm -rf $$dir; \
		echo "  -> $(BUILD_DIR)/watchfire-linux-$$arch.tar.gz"; \
	done

# ── Utilities ────────────────────────────────────────────────

sync-version:
	@echo "Syncing version to gui/package.json..."
	jq --arg v "$(VERSION)" '.version = $$v' gui/package.json > gui/package.json.tmp && mv gui/package.json.tmp gui/package.json

proto:
	@echo "Generating protobuf code..."
	protoc --go_out=. --go_opt=paths=source_relative \
		--go-grpc_out=. --go-grpc_opt=paths=source_relative \
		proto/watchfire.proto

test:
	$(GOTEST) -v -race ./...

lint:
	golangci-lint run ./...

clean:
	rm -rf $(BUILD_DIR) tmp gui/out gui/dist gui/node_modules/.cache

install-tools:
	@echo "Installing development tools..."
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	go install github.com/air-verse/air@latest
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
	@echo "Tools installed."

tidy:
	$(GOMOD) tidy

uninstall:
	@echo "Removing watchfire from /usr/local/bin..."
	@rm -f /usr/local/bin/$(CLI_BINARY)    2>/dev/null || sudo rm -f /usr/local/bin/$(CLI_BINARY)
	@rm -f /usr/local/bin/$(DAEMON_BINARY) 2>/dev/null || sudo rm -f /usr/local/bin/$(DAEMON_BINARY)
	@rm -rf /Applications/Watchfire.app    2>/dev/null || sudo rm -rf /Applications/Watchfire.app
	@echo "Uninstalled."
