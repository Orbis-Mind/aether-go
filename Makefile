.PHONY: build build-all build-windows build-mac build-linux run test clean deps

VERSION ?= 0.1.0
LDFLAGS = -ldflags="-s -w -X main.version=$(VERSION)"

# Build for current OS
build:
	go build $(LDFLAGS) -o dist/aether ./cmd/aether

# Build for all platforms
build-all: build-windows build-mac-arm build-mac-intel build-linux

build-windows:
	GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o dist/aether.exe ./cmd/aether

build-mac-arm:
	GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o dist/aether-mac-arm ./cmd/aether

build-mac-intel:
	GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o dist/aether-mac-intel ./cmd/aether

build-linux:
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o dist/aether-linux ./cmd/aether

# Install locally
install: build
	cp dist/aether /usr/local/bin/aether

# Run tests
test:
	go test ./... -v

# Download dependencies
deps:
	go mod tidy
	go mod download

# Demo run
demo: build
	@mkdir -p /tmp/aether-demo
	$(eval AETHER := $(abspath dist/aether))
	$(AETHER) init demo-project --dir /tmp/aether-demo/demo-project
	cd /tmp/aether-demo/demo-project && $(AETHER) build
	cd /tmp/aether-demo/demo-project && $(AETHER) test
	cd /tmp/aether-demo/demo-project && $(AETHER) run --mock --message "Hello Aether"
	cd /tmp/aether-demo/demo-project && $(AETHER) benchmark --runs 5
	cd /tmp/aether-demo/demo-project && $(AETHER) stats

# Clean build artifacts
clean:
	rm -rf dist/
	go clean

# Check binary size
size:
	@ls -lh dist/

help:
	@echo "Aether Go Build Commands:"
	@echo "  make build          Build for current OS"
	@echo "  make build-all      Build for Windows, Mac, Linux"
	@echo "  make build-windows  Build aether.exe for Windows"
	@echo "  make build-mac-arm  Build for Mac Apple Silicon"
	@echo "  make build-linux    Build for Linux x64"
	@echo "  make install        Install to /usr/local/bin"
	@echo "  make test           Run tests"
	@echo "  make deps           Download dependencies"
	@echo "  make demo           Run full demo"
	@echo "  make clean          Remove build artifacts"
