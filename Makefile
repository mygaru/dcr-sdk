SHELL := /bin/bash

MODULE := github.com/mygaru/dcr-sdk
PROTO_DIR := base/v1
PROTO_FILES := $(wildcard $(PROTO_DIR)/*.proto)

GO ?= go
PROTOC ?= protoc
GOLANGCI_LINT ?= golangci-lint

.PHONY: help
help:
	@echo "Available targets:"
	@echo "  proto        - generate protobuf code"
	@echo "  proto-clean  - remove generated protobuf files"
	@echo "  fmt          - format Go code"
	@echo "  test         - run tests"
	@echo "  test-race    - run tests with race detector"
	@echo "  vet          - run go vet"
	@echo "  lint         - run golangci-lint"
	@echo "  tidy         - run go mod tidy"
	@echo "  check        - fmt + vet + test"
	@echo "  all          - proto + fmt + vet + test"

.PHONY: proto
proto:
	$(PROTOC) -I . \
		--go_out=. \
		--go_opt=module=$(MODULE) \
		--go-grpc_out=. \
		--go-grpc_opt=module=$(MODULE) \
		$(PROTO_FILES)

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