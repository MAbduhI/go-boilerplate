# Workspace-wide tasks.
#
# A Go workspace has no root module, so `go build ./...` from here cannot see the
# member modules. Every target therefore loops over the modules listed in
# go.work, which keeps `make build` correct as modules are added by `gen`.

SHELL := /bin/bash
GOBIN ?= $(shell go env GOPATH)/bin

# Module directories, read straight from go.work so nothing needs updating when
# `gen make:module` adds one.
MODULES := $(shell grep -oE '\./[A-Za-z0-9_./-]+' go.work)

GOLANGCI_VERSION := v2.12.2

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@echo "Targets:"
	@grep -hE '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| sort \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'
	@echo
	@echo "Modules: $(MODULES)"

.PHONY: build
build: ## Build every module
	@for m in $(MODULES); do \
		echo "==> build $$m"; \
		(cd $$m && go build ./...) || exit 1; \
	done

.PHONY: vet
vet: ## Run go vet on every module
	@for m in $(MODULES); do \
		echo "==> vet $$m"; \
		(cd $$m && go vet ./...) || exit 1; \
	done

.PHONY: test
test: ## Run tests in every module
	@for m in $(MODULES); do \
		echo "==> test $$m"; \
		(cd $$m && go test ./...) || exit 1; \
	done

.PHONY: test-race
test-race: ## Run tests with the race detector (needs cgo + a C compiler)
	@for m in $(MODULES); do \
		echo "==> test -race $$m"; \
		(cd $$m && CGO_ENABLED=1 go test -race ./...) || exit 1; \
	done

.PHONY: test-integration
test-integration: ## Run tests that need live services (make up first)
	@for m in $(MODULES); do \
		echo "==> test -tags integration $$m"; \
		(cd $$m && go test -tags integration ./...) || exit 1; \
	done

.PHONY: cover
cover: ## Run tests with a coverage summary
	@for m in $(MODULES); do \
		echo "==> cover $$m"; \
		(cd $$m && go test -coverprofile=coverage.out ./... && go tool cover -func=coverage.out | tail -1) || exit 1; \
	done

.PHONY: fmt
fmt: ## Format all Go code
	@gofmt -l -w $(MODULES)
	@echo "formatted"

.PHONY: fmt-check
fmt-check: ## Fail if any file is unformatted
	@out=$$(gofmt -l $(MODULES)); \
	if [ -n "$$out" ]; then echo "unformatted files:"; echo "$$out"; exit 1; fi; \
	echo "all files formatted"

.PHONY: lint
lint: ## Run golangci-lint on every module
	@command -v golangci-lint >/dev/null 2>&1 || { \
		echo "golangci-lint not found; run 'make tools'"; exit 1; }
	@for m in $(MODULES); do \
		echo "==> lint $$m"; \
		(cd $$m && golangci-lint run --config $(CURDIR)/.golangci.yml ./...) || exit 1; \
	done

.PHONY: lint-fix
lint-fix: ## Run golangci-lint with --fix
	@for m in $(MODULES); do \
		echo "==> lint --fix $$m"; \
		(cd $$m && golangci-lint run --config $(CURDIR)/.golangci.yml --fix ./...) || exit 1; \
	done

.PHONY: tidy
tidy: ## Tidy each module (see docs/ARCHITECTURE.md for the workspace caveat)
	@echo "NOTE: modules import sibling workspace modules without a require line,"
	@echo "      so 'go mod tidy' cannot resolve them and would add a bad require."
	@echo "      Only the shared module is tidied here."
	@(cd shared && GOWORK=off go mod tidy)
	@(cd cmd/gen && GOWORK=off go mod tidy)

.PHONY: gen
gen: ## Install the code generator as 'gen'
	go install ./cmd/gen
	@echo "installed $(GOBIN)/gen"

.PHONY: proto
proto: gen ## Compile every modules/*/grpc/*.proto
	$(GOBIN)/gen proto:build

.PHONY: run
run: ## Run the API
	@(cd app && go run ./cmd/api)

.PHONY: tools
tools: ## Install the development toolchain
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION)
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
	go install ./cmd/gen
	@echo "tools installed to $(GOBIN)"

.PHONY: up
up: ## Start the local dev services
	docker compose up -d

.PHONY: down
down: ## Stop the local dev services
	docker compose down

.PHONY: logs
logs: ## Tail the dev service logs
	docker compose logs -f

.PHONY: check
check: fmt-check vet lint test ## Everything CI runs
	@echo "all checks passed"

.PHONY: clean
clean: ## Remove build and coverage artefacts
	@find . -name coverage.out -delete
	@go clean -cache -testcache
	@echo "cleaned"
