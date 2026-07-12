SHELL := /bin/bash
.DEFAULT_GOAL := help
.DELETE_ON_ERROR:
.NOTPARALLEL: check

PROJECT := jobman
MODULE := github.com/ryancswallace/jobman

BIN_DIR := bin
DIST_DIR := dist
DOCS_DIR := docs
MANPAGE_DIR := $(DOCS_DIR)/manpage
COMPLETIONS_DIR := $(DOCS_DIR)/completions
COVERAGE_FILE := coverage.txt
COVERAGE_HTML := coverage.html

GEN_MANPAGE := ./devel/manpages/manpages.go
GEN_COMPLETIONS := ./devel/autocomplete/autocomplete.go
UPDATE_SCRIPTS := ./devel/updates

GO ?= go
DOCKER ?= docker
CURL ?= curl

GO_VERSION := $(shell tr -d '[:space:]' < go.version)
GOLANGCI_LINT_VERSION ?= v2.12.2
GORELEASER_VERSION ?= v2.17.0
CSPELL_VERSION ?= 10.0.1

GOLANGCI_LINT ?= $(shell command -v golangci-lint 2>/dev/null || printf '%s' '$(BIN_DIR)/golangci-lint')
GORELEASER ?= $(shell command -v goreleaser 2>/dev/null || printf '%s' '$(BIN_DIR)/goreleaser')

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || printf '%s' dev)
COMMIT ?= $(shell git rev-parse HEAD 2>/dev/null || printf '%s' unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
IMAGE ?= $(PROJECT):local

GO_BUILD_FLAGS ?= -trimpath
GO_LDFLAGS ?= -s -w -buildid=
GO_TEST_FLAGS ?= -race -shuffle=on

.PHONY: help
help: ## Show available targets.
	@awk 'BEGIN {FS = ":.*## "; printf "Usage: make <target>\n\nTargets:\n"} /^[a-zA-Z0-9_.-]+:.*## / {printf "  %-22s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.PHONY: all
all: check ## Run the complete local verification workflow.

.PHONY: setup bootstrap
setup: bootstrap ## Install tools and download Go modules.
bootstrap: tools download

.PHONY: tools
tools: tool-golangci-lint tool-goreleaser ## Install pinned development tools into bin/ when absent.

.PHONY: tool-golangci-lint
tool-golangci-lint:
	@if ! $(GOLANGCI_LINT) version >/dev/null 2>&1; then \
		echo "Installing golangci-lint $(GOLANGCI_LINT_VERSION) into $(BIN_DIR)/"; \
		mkdir -p $(BIN_DIR); \
		$(CURL) -sSfL https://golangci-lint.run/install.sh \
			| sh -s -- -b $(abspath $(BIN_DIR)) $(GOLANGCI_LINT_VERSION); \
	fi

.PHONY: tool-goreleaser
tool-goreleaser:
	@if ! $(GORELEASER) --version >/dev/null 2>&1; then \
		echo "Installing GoReleaser $(GORELEASER_VERSION) into $(BIN_DIR)/"; \
		mkdir -p $(BIN_DIR); \
		GOBIN=$(abspath $(BIN_DIR)) $(GO) install \
			github.com/goreleaser/goreleaser/v2@$(GORELEASER_VERSION); \
	fi

.PHONY: versions
versions: ## Print the versions used by development and release tooling.
	@printf 'project:        %s\n' '$(VERSION)'
	@printf 'go requested:   %s\n' '$(GO_VERSION)'
	@$(GO) version
	@$(MAKE) --no-print-directory tool-golangci-lint
	@$(GOLANGCI_LINT) version
	@$(MAKE) --no-print-directory tool-goreleaser
	@$(GORELEASER) --version

.PHONY: download
download: ## Download and verify Go module dependencies.
	$(GO) mod download
	$(GO) mod verify

.PHONY: tidy
tidy: ## Update go.mod and go.sum to match the source tree.
	$(GO) mod tidy

.PHONY: mod-check
mod-check: ## Verify module files are tidy without changing them.
	$(GO) mod verify
	$(GO) mod tidy -diff

.PHONY: format fmt
format: tool-golangci-lint ## Format Go source with the configured formatters.
	$(GOLANGCI_LINT) fmt
fmt: format

.PHONY: format-check
format-check: tool-golangci-lint ## Check formatting without changing files.
	$(GOLANGCI_LINT) fmt --diff

.PHONY: lint
lint: tool-golangci-lint ## Run the configured Go linters.
	$(GOLANGCI_LINT) run ./...

.PHONY: vet
vet: ## Run go vet independently of the aggregate linter.
	$(GO) vet ./...

.PHONY: unittest unit
unittest: ## Run unit tests with race detection and coverage.
	$(GO) test $(GO_TEST_FLAGS) -covermode=atomic -coverprofile=$(COVERAGE_FILE) ./...
unit: unittest

.PHONY: e2etest e2e
e2etest: ## Run end-to-end tests when the suite contains Go tests.
	@if find tests/e2e -type f -name '*_test.go' -print -quit | grep -q .; then \
		$(GO) test $(GO_TEST_FLAGS) ./tests/e2e/...; \
	else \
		echo 'No end-to-end tests are implemented yet; skipping.'; \
	fi
e2e: e2etest

.PHONY: perftest bench
perftest: ## Run benchmarks when the performance suite contains Go tests.
	@if find tests/perf -type f -name '*_test.go' -print -quit | grep -q .; then \
		$(GO) test -run '^$$' -bench . -benchmem ./tests/perf/...; \
	else \
		echo 'No performance tests are implemented yet; skipping.'; \
	fi
bench: perftest

.PHONY: test
test: unittest e2etest perftest ## Run unit, end-to-end, and performance tests.

.PHONY: coverage
coverage: unittest ## Generate the coverage profile.
	$(GO) tool cover -func=$(COVERAGE_FILE)

.PHONY: coverage-html
coverage-html: unittest ## Generate an HTML coverage report.
	$(GO) tool cover -html=$(COVERAGE_FILE) -o $(COVERAGE_HTML)
	@echo "Wrote $(COVERAGE_HTML)"

.PHONY: gen-manpage
gen-manpage: ## Generate command man pages.
	$(GO) run $(GEN_MANPAGE)
	@test -s $(MANPAGE_DIR)/$(PROJECT).1

.PHONY: gen-completions
gen-completions: ## Generate Bash, Zsh, and PowerShell completions.
	$(GO) run $(GEN_COMPLETIONS)
	@test -s $(COMPLETIONS_DIR)/bash/$(PROJECT)
	@test -s $(COMPLETIONS_DIR)/zsh/_$(PROJECT)
	@test -s $(COMPLETIONS_DIR)/powershell/$(PROJECT).ps1

.PHONY: gen-all generate
gen-all: gen-manpage gen-completions ## Generate every derived documentation asset.
generate: gen-all

.PHONY: docs-check
docs-check: ## Check Markdown whitespace and generated documentation assets.
	@! find . -path './.git' -prune -o -type f -name '*.md' -exec grep -nH -E '[[:blank:]]+$$' {} + | grep .
	@test -s $(MANPAGE_DIR)/$(PROJECT).1
	@test -s $(COMPLETIONS_DIR)/bash/$(PROJECT)
	@test -s $(COMPLETIONS_DIR)/zsh/_$(PROJECT)
	@test -s $(COMPLETIONS_DIR)/powershell/$(PROJECT).ps1

.PHONY: spellcheck
spellcheck: ## Spell-check the repository using cspell or its pinned container.
	@if command -v cspell >/dev/null 2>&1; then \
		cspell lint .; \
	elif command -v npx >/dev/null 2>&1; then \
		npx --yes cspell@$(CSPELL_VERSION) lint .; \
	elif $(DOCKER) info >/dev/null 2>&1; then \
		$(DOCKER) build --file Dockerfile.cspell \
			--build-arg CSPELL_VERSION=$(CSPELL_VERSION) \
			--output type=cacheonly .; \
	else \
		echo 'cspell requires cspell, npx, or a running Docker daemon.' >&2; \
		exit 2; \
	fi

.PHONY: docs
docs: gen-all docs-check spellcheck ## Generate and validate documentation.

.PHONY: build
build: ## Build the jobman binary for the current platform.
	mkdir -p $(BIN_DIR)
	$(GO) build $(GO_BUILD_FLAGS) -ldflags='$(GO_LDFLAGS)' -o $(BIN_DIR)/$(PROJECT) .

.PHONY: install
install: ## Install jobman with the active Go toolchain.
	$(GO) install $(GO_BUILD_FLAGS) -ldflags='$(GO_LDFLAGS)' .

.PHONY: run
run: build ## Build and run jobman; pass arguments with ARGS='...'.
	$(BIN_DIR)/$(PROJECT) $(ARGS)

.PHONY: docker-check
docker-check: ## Validate the Dockerfile without building an image.
	$(DOCKER) build --check .

.PHONY: docker-image
docker-image: ## Build the local container image.
	$(DOCKER) build \
		--build-arg VERSION='$(VERSION)' \
		--build-arg VCS_REF='$(COMMIT)' \
		--build-arg BUILD_DATE='$(BUILD_DATE)' \
		--tag $(IMAGE) \
		.

.PHONY: docker-run
docker-run: docker-image ## Run the local image; pass jobman arguments with ARGS='...'.
	$(DOCKER) run --rm $(DOCKER_RUN_FLAGS) $(IMAGE) $(ARGS)

.PHONY: build-all
build-all: build docker-image ## Build the local binary and container image.

.PHONY: release-check
release-check: tool-goreleaser ## Validate the GoReleaser configuration.
	$(GORELEASER) check

.PHONY: snapshot
snapshot: tool-goreleaser ## Build a local release snapshot without publishing.
	$(GORELEASER) release --snapshot --clean --skip=sign,homebrew

.PHONY: check quick-check ci
check: mod-check format-check lint test docs build release-check ## Run all presubmission checks.
quick-check: mod-check format-check unittest build ## Run the fast presubmission checks.
ci: check ## Alias for the complete CI verification workflow.

.PHONY: update
update: ## Run repository maintenance scripts.
	GO_VERS=$(GO_VERSION) run-parts $(UPDATE_SCRIPTS)

.PHONY: update-all
update-all: update format gen-all ## Run maintenance, formatting, and generation.

.PHONY: clean-generated
clean-generated: ## Remove generated man pages and completion scripts.
	$(RM) $(MANPAGE_DIR)/$(PROJECT)*.1
	$(RM) $(COMPLETIONS_DIR)/bash/$(PROJECT)
	$(RM) $(COMPLETIONS_DIR)/zsh/_$(PROJECT)
	$(RM) $(COMPLETIONS_DIR)/powershell/$(PROJECT).ps1

.PHONY: clean
clean: clean-generated ## Remove build, release, and test artifacts.
	$(RM) -r $(BIN_DIR) $(DIST_DIR)
	$(RM) $(COVERAGE_FILE) $(COVERAGE_HTML)

.PHONY: clean-tools
clean-tools: ## Remove tools installed into bin/ by this Makefile.
	$(RM) $(BIN_DIR)/golangci-lint $(BIN_DIR)/goreleaser
