BINARY := dnsbench
BUILD_DIR := releases
GOOS ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)
BINARY_EXT := $(if $(filter windows,$(GOOS)),.exe,)
OUTPUT := $(BUILD_DIR)/$(GOOS)/$(GOARCH)/$(BINARY)$(BINARY_EXT)

.PHONY: build

build:
	@mkdir -p $(dir $(OUTPUT))
	GOOS=$(GOOS) GOARCH=$(GOARCH) go build -o $(OUTPUT) ./cmd/dnsbench
	@if [ "$(GOOS)" = "darwin" ] && command -v codesign >/dev/null 2>&1; then codesign --force --sign - "$(OUTPUT)"; fi
