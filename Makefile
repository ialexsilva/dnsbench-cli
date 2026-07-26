BINARY := dnsbench
BUILD_DIR := releases
GOOS ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)
BINARY_EXT := $(if $(filter windows,$(GOOS)),.exe,)
OUTPUT := $(BUILD_DIR)/$(GOOS)/$(GOARCH)/$(BINARY)$(BINARY_EXT)

# -s drops the symbol table and -w the DWARF debug info, together about 30% of
# the file. Panic tracebacks keep function names and line numbers because the Go
# runtime reads those from pclntab, which neither flag touches; only attaching a
# debugger to a release binary stops working. Override with LDFLAGS= to keep them.
LDFLAGS ?= -s -w

.PHONY: build

build:
	@mkdir -p $(dir $(OUTPUT))
	GOOS=$(GOOS) GOARCH=$(GOARCH) go build -ldflags="$(LDFLAGS)" -o $(OUTPUT) ./cmd/dnsbench
	@if [ "$(GOOS)" = "darwin" ] && command -v codesign >/dev/null 2>&1; then codesign --force --sign - "$(OUTPUT)"; fi
