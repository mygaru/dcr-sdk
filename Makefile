SHELL := /bin/bash

MODULE := github.com/mygaru/dcr-sdk

# PROTO_GO_MODULE is the module prefix protoc strips from `option go_package` to
# place the generated files, so it must match the go_package declared in
# base/v1/*.proto. That go_package deliberately names the PUBLIC module path
# rather than $(MODULE): protoc embeds it, length-prefixed, into the descriptor
# inside each *.pb.go, and the sync_to_github CI job sed-rewrites the internal
# path to the public one across every text file. On a *.pb.go that shortens the
# embedded string without fixing its length prefix and produces a descriptor that
# panics at init. Generating with the public path keeps the internal path out of
# the descriptor, so the mirror has nothing to rewrite.
PROTO_GO_MODULE := github.com/mygaru/dcr-sdk

PROTO_DIR := base/v1
PROTO_FILES := $(wildcard $(PROTO_DIR)/*.proto)
GEN_DIR := gen

# Pinned to the google.golang.org/protobuf version in go.mod. Regenerating with a
# different minor version rewrites every file for no reason.
PROTOC_GEN_GO_VERSION := v1.36.11
PROTOC_GEN_GO_GRPC_VERSION := v1.5.1

GO ?= go
PROTOC ?= protoc
GOLANGCI_LINT ?= golangci-lint

.PHONY: help
help:
	@echo "Available targets:"
	@echo "  proto-tools  - install the pinned protoc-gen-go plugins"
	@echo "  proto        - generate protobuf code (runs proto-verify)"
	@echo "  proto-verify - check the generated descriptors are mirror-safe and load"
	@echo "  proto-clean  - remove generated protobuf files"
	@echo "  fmt          - format Go code"
	@echo "  test         - run tests"
	@echo "  test-race    - run tests with race detector"
	@echo "  vet          - run go vet"
	@echo "  lint         - run golangci-lint"
	@echo "  tidy         - run go mod tidy"
	@echo "  check        - fmt + vet + test"
	@echo "  all          - proto + fmt + vet + test"

.PHONY: proto-tools
proto-tools:
	$(GO) install google.golang.org/protobuf/cmd/protoc-gen-go@$(PROTOC_GEN_GO_VERSION)
	$(GO) install google.golang.org/grpc/cmd/protoc-gen-go-grpc@$(PROTOC_GEN_GO_GRPC_VERSION)

.PHONY: proto
proto:
	$(PROTOC) -I . \
		--go_out=. \
		--go_opt=module=$(PROTO_GO_MODULE) \
		--go-grpc_out=. \
		--go-grpc_opt=module=$(PROTO_GO_MODULE) \
		$(PROTO_FILES)
	$(MAKE) proto-verify

# proto-verify guards the two ways a generated descriptor has silently broken
# every consumer of this SDK before:
#   1. the internal module path leaking into a *.pb.go, where the mirror's sed
#      corrupts its length prefix;
#   2. a descriptor that no longer parses, which panics at package init rather
#      than failing the build.
.PHONY: proto-verify
proto-verify:
	@if grep -rn "$(MODULE)" $(GEN_DIR) ; then \
		echo "ERROR: internal module path found in generated code."; \
		echo "       The sync_to_github job rewrites it with sed and corrupts the"; \
		echo "       embedded descriptor. Generated files must carry only"; \
		echo "       $(PROTO_GO_MODULE). Check option go_package in $(PROTO_DIR)/*.proto."; \
		exit 1; \
	fi
	@$(GO) build ./$(GEN_DIR)/...
	@$(GO) run ./cmd/proto-verify
	@echo "proto-verify: generated descriptors are mirror-safe and load correctly"

.PHONY: proto-clean
proto-clean:
	find gen -type f \( -name "*.pb.go" -o -name "*_grpc.pb.go" \) -delete

.PHONY: fmt
fmt:
	$(GO) fmt ./...

.PHONY: test
test:
	$(GO) test ./...

.PHONY: test-race
test-race:
	$(GO) test -race ./...

.PHONY: vet
vet:
	$(GO) vet ./...

.PHONY: lint
lint:
	$(GOLANGCI_LINT) run ./...

.PHONY: tidy
tidy:
	$(GO) mod tidy

.PHONY: check
check: fmt vet test

.PHONY: all
all: proto fmt vet test