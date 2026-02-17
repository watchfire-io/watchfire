.PHONY: dev-daemon dev-tui dev-gui build build-daemon build-cli test lint proto clean install-tools \
       build-daemon-arm64 build-daemon-amd64 build-cli-arm64 build-cli-amd64 build-universal sync-version package-gui

# Binary names
DAEMON_BINARY=watchfired
CLI_BINARY=watchfire

# Build directories
BUILD_DIR=build
CMD_DIR=cmd

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
GOMOD=$(GOCMD) mod

# Version info from version.json
VERSION := $(shell python3 -c "import json; print(json.load(open('version.json'))['version'])")
CODENAME := $(shell python3 -c "import json; print(json.load(open('version.json'))['codename'])")
COMMIT := $(shell git rev-parse --short HEAD)
BUILD_DATE := $(shell date -u +%Y-%m-%d)

# Ldflags for version injection
LDFLAGS := -X github.com/watchfire-io/watchfire/internal/buildinfo.Version=$(VERSION) \
           -X github.com/watchfire-io/watchfire/internal/buildinfo.Codename=$(CODENAME) \
           -X github.com/watchfire-io/watchfire/internal/buildinfo.CommitHash=$(COMMIT) \
           -X github.com/watchfire-io/watchfire/internal/buildinfo.BuildDate=$(BUILD_DATE)

# Development with hot reload
dev-daemon:
	@echo "Starting daemon with hot reload..."
	air -c .air.toml

# Build and run TUI
dev-tui: build-cli
	./$(BUILD_DIR)/$(CLI_BINARY)

# Run Electron dev mode
dev-gui:
	cd gui && npm run dev

# Build all Go binaries (native arch)
build: build-daemon build-cli

build-daemon:
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=1 $(GOBUILD) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(DAEMON_BINARY) ./$(CMD_DIR)/watchfired

build-cli:
	@mkdir -p $(BUILD_DIR)
	$(GOBUILD) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(CLI_BINARY) ./$(CMD_DIR)/watchfire

# Architecture-specific builds
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

# Universal (fat) binaries via lipo — for release packaging
build-universal: build-daemon-arm64 build-daemon-amd64 build-cli-arm64 build-cli-amd64
	@echo "Creating universal binaries..."
	lipo -create -output $(BUILD_DIR)/$(DAEMON_BINARY) $(BUILD_DIR)/$(DAEMON_BINARY)-arm64 $(BUILD_DIR)/$(DAEMON_BINARY)-amd64
	lipo -create -output $(BUILD_DIR)/$(CLI_BINARY) $(BUILD_DIR)/$(CLI_BINARY)-arm64 $(BUILD_DIR)/$(CLI_BINARY)-amd64
	@echo "Universal binaries created."
	lipo -info $(BUILD_DIR)/$(DAEMON_BINARY)
	lipo -info $(BUILD_DIR)/$(CLI_BINARY)

# Sync version.json → gui/package.json
sync-version:
	@echo "Syncing version to gui/package.json..."
	jq --arg v "$(VERSION)" '.version = $$v' gui/package.json > gui/package.json.tmp && mv gui/package.json.tmp gui/package.json

# Run tests
test:
	$(GOTEST) -v -race ./...

# Run linter
lint:
	golangci-lint run ./...

# Generate protobuf code
proto:
	@echo "Generating protobuf code..."
	protoc --go_out=. --go_opt=paths=source_relative \
		--go-grpc_out=. --go-grpc_opt=paths=source_relative \
		proto/watchfire.proto

# Build GUI (Electron)
build-gui:
	cd gui && npm run build

# Package GUI as distributable (builds universal Go binaries first)
package-gui: sync-version build-universal build-gui
	cd gui && npm run package

# Clean build artifacts
clean:
	rm -rf $(BUILD_DIR)
	rm -rf tmp
	rm -rf gui/out gui/dist gui/node_modules/.cache

# Install development tools
install-tools:
	@echo "Installing development tools..."
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	go install github.com/air-verse/air@latest
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
	@echo "Tools installed successfully"

# Tidy dependencies
tidy:
	$(GOMOD) tidy

# Run daemon in foreground (no hot reload)
run-daemon: build-daemon
	./$(BUILD_DIR)/$(DAEMON_BINARY) -foreground

# Run CLI
run-cli: build-cli
	./$(BUILD_DIR)/$(CLI_BINARY)
