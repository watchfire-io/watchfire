.PHONY: dev-daemon dev-tui dev-gui build build-daemon build-cli test lint proto clean install-tools

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

# Development with hot reload
dev-daemon:
	@echo "Starting daemon with hot reload..."
	air -c .air.toml

# Build and run TUI
dev-tui: build-cli
	./$(BUILD_DIR)/$(CLI_BINARY)

# Run Electron dev mode (placeholder)
dev-gui:
	@echo "GUI not yet implemented"
	@echo "Run: cd gui && npm run dev"

# Build all Go binaries
build: build-daemon build-cli

build-daemon:
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=1 $(GOBUILD) -o $(BUILD_DIR)/$(DAEMON_BINARY) ./$(CMD_DIR)/watchfired

build-cli:
	@mkdir -p $(BUILD_DIR)
	$(GOBUILD) -o $(BUILD_DIR)/$(CLI_BINARY) ./$(CMD_DIR)/watchfire

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

# Clean build artifacts
clean:
	rm -rf $(BUILD_DIR)
	rm -rf tmp

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
