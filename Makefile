SHELL := /bin/bash
.DEFAULT_GOAL := help
.DELETE_ON_ERROR:
.NOTPARALLEL: check

# Validation targets must not take over the terminal with an interactive pager.
# Individual commands can still opt into one outside the Makefile.
PAGER := cat
GIT_PAGER := cat
GH_PAGER := cat
MANPAGER := cat
SYSTEMD_PAGER := cat
export PAGER GIT_PAGER GH_PAGER MANPAGER SYSTEMD_PAGER

PROJECT := jobman
MODULE := github.com/ryancswallace/jobman

BIN_DIR := bin
DIST_DIR := dist
DOCS_DIR := docs
MANPAGE_DIR := $(DOCS_DIR)/manpage
COMPLETIONS_DIR := $(DOCS_DIR)/completions
COVERAGE_FILE := coverage.txt
COVERAGE_RAW := coverage.raw
COVERAGE_HTML := coverage.html
COVERAGE_MIN ?= 90

GEN_MANPAGE := ./devel/manpages/manpages.go
GEN_COMPLETIONS := ./devel/autocomplete/autocomplete.go
UPDATE_SCRIPTS := ./devel/updates

GO ?= go
DOCKER ?= docker
DOCKER_PROGRESS ?= plain

GO_VERSION := $(shell tr -d '[:space:]' < go.version)
GOLANGCI_LINT_VERSION ?= v2.12.2
GORELEASER_VERSION ?= v2.17.0
ACTIONLINT_VERSION ?= v1.7.12
GOVULNCHECK_VERSION ?= v1.6.0
SYFT_VERSION ?= v1.46.0
CSPELL_VERSION ?= 10.0.1

GOLANGCI_LINT ?= $(BIN_DIR)/golangci-lint
GORELEASER ?= $(BIN_DIR)/goreleaser
ACTIONLINT ?= $(BIN_DIR)/actionlint
GOVULNCHECK ?= $(BIN_DIR)/govulncheck
SYFT ?= $(BIN_DIR)/syft
SYFT_VERSION_FILE := $(BIN_DIR)/.syft-$(SYFT_VERSION)

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || printf '%s' dev)
COMMIT ?= $(shell git rev-parse HEAD 2>/dev/null || printf '%s' unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
IMAGE ?= $(PROJECT):local

GO_BUILD_FLAGS ?= -trimpath
GO_LDFLAGS ?= -s -w -buildid= \
	-X github.com/ryancswallace/jobman/internal/buildinfo.Version=$(VERSION) \
	-X github.com/ryancswallace/jobman/internal/buildinfo.Commit=$(COMMIT) \
	-X github.com/ryancswallace/jobman/internal/buildinfo.Date=$(BUILD_DATE)
GO_TEST_FLAGS ?= -race -shuffle=on
FUZZ_PACKAGE ?= ./internal/model
FUZZ_TARGET ?= FuzzParseJobSpecJSON
FUZZ_TIME ?= 30s
FUZZ_PARALLEL ?= 4
PERF_TIME ?= 1s
SOAK_TIME ?= 10m
SOAK_TIMEOUT ?= 15m

.PHONY: help
help: ## Show available targets.
	@awk 'BEGIN {FS = ":.*## "; printf "Usage: make <target>\n\nTargets:\n"} /^[a-zA-Z0-9_.-]+:.*## / {printf "  %-22s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.PHONY: all
all: check ## Run the complete local verification workflow.

.PHONY: setup bootstrap
setup: bootstrap ## Install tools and download Go modules.
bootstrap: tools download

.PHONY: tools
tools: tool-golangci-lint tool-goreleaser tool-actionlint tool-govulncheck tool-syft ## Install pinned development tools into bin/ when absent.

.PHONY: tool-golangci-lint
tool-golangci-lint:
	@if ! $(GOLANGCI_LINT) version 2>/dev/null \
		| grep -Fq 'version $(patsubst v%,%,$(GOLANGCI_LINT_VERSION))'; then \
		echo "Installing golangci-lint $(GOLANGCI_LINT_VERSION) into $(BIN_DIR)/"; \
		mkdir -p $(BIN_DIR); \
		GOBIN=$(abspath $(BIN_DIR)) $(GO) install \
			github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION); \
	fi

.PHONY: tool-goreleaser
tool-goreleaser:
	@if ! $(GORELEASER) --version 2>/dev/null \
		| grep -Fq '$(patsubst v%,%,$(GORELEASER_VERSION))'; then \
		echo "Installing GoReleaser $(GORELEASER_VERSION) into $(BIN_DIR)/"; \
		mkdir -p $(BIN_DIR); \
		GOBIN=$(abspath $(BIN_DIR)) $(GO) install \
			github.com/goreleaser/goreleaser/v2@$(GORELEASER_VERSION); \
	fi

.PHONY: tool-actionlint
tool-actionlint:
	@if ! $(ACTIONLINT) -version 2>/dev/null \
		| grep -Fq '$(patsubst v%,%,$(ACTIONLINT_VERSION))'; then \
		echo "Installing actionlint $(ACTIONLINT_VERSION) into $(BIN_DIR)/"; \
		mkdir -p $(BIN_DIR); \
		GOBIN=$(abspath $(BIN_DIR)) $(GO) install \
			github.com/rhysd/actionlint/cmd/actionlint@$(ACTIONLINT_VERSION); \
	fi

.PHONY: tool-govulncheck
tool-govulncheck:
	@if ! $(GOVULNCHECK) -version 2>/dev/null \
		| grep -Fq '$(GOVULNCHECK_VERSION)'; then \
		echo "Installing govulncheck $(GOVULNCHECK_VERSION) into $(BIN_DIR)/"; \
		mkdir -p $(BIN_DIR); \
		GOBIN=$(abspath $(BIN_DIR)) $(GO) install \
			golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION); \
	fi

.PHONY: tool-syft
tool-syft:
	@if ! test -x '$(SYFT)' || ! test -f '$(SYFT_VERSION_FILE)'; then \
		echo "Installing Syft $(SYFT_VERSION) into $(BIN_DIR)/"; \
		mkdir -p $(BIN_DIR); \
		GOBIN=$(abspath $(BIN_DIR)) $(GO) install \
			github.com/anchore/syft/cmd/syft@$(SYFT_VERSION); \
		touch '$(SYFT_VERSION_FILE)'; \
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
	@$(MAKE) --no-print-directory tool-actionlint
	@$(ACTIONLINT) -version
	@$(MAKE) --no-print-directory tool-govulncheck
	@$(GOVULNCHECK) -version
	@$(MAKE) --no-print-directory tool-syft
	@$(SYFT) version

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

.PHONY: workflow-check
workflow-check: tool-actionlint ## Validate all GitHub Actions workflows.
	$(ACTIONLINT) .github/workflows/*.yml

.PHONY: shellcheck
shellcheck: ## Statically analyze repository shell scripts.
	@if command -v shellcheck >/dev/null 2>&1; then \
		shellcheck devel/updates/*.sh; \
	elif $(DOCKER) info >/dev/null 2>&1; then \
		$(DOCKER) run --rm -v '$(CURDIR):/src:ro' -w /src \
			koalaman/shellcheck-alpine:v0.11.0 devel/updates/*.sh; \
	else \
		echo 'shellcheck requires shellcheck or a running Docker daemon.' >&2; \
		exit 2; \
	fi

.PHONY: vulncheck
vulncheck: tool-govulncheck ## Check reachable Go code for known vulnerabilities.
	$(GOVULNCHECK) ./...

.PHONY: vet
vet: ## Run go vet independently of the aggregate linter.
	$(GO) vet ./...

.PHONY: unittest unit
unittest: ## Run unit tests with race detection and coverage.
	@set -eu; \
	trap '$(RM) $(COVERAGE_RAW)' EXIT; \
	packages="$$( $(GO) list ./... \
		| grep -Ev '/tests/(e2e|perf)($$|/)' )"; \
	$(GO) test $(GO_TEST_FLAGS) -covermode=atomic -coverpkg=./... \
		-coverprofile=$(COVERAGE_RAW) $$packages; \
	awk -v minimum='$(COVERAGE_MIN)' -v output='$(COVERAGE_FILE)' \
		-f devel/check-coverage.awk $(COVERAGE_RAW)
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
		$(GO) test -run '^TestPerformanceContract' ./tests/perf/...; \
		$(GO) test -run '^$$' -bench '^Benchmark' -benchtime='$(PERF_TIME)' \
			-benchmem ./tests/perf/...; \
	else \
		echo 'No performance tests are implemented yet; skipping.'; \
	fi
bench: perftest

.PHONY: soaktest soak
soaktest: ## Run opt-in concurrent storage/log/admission soak tests.
	JOBMAN_SOAK=1 JOBMAN_SOAK_DURATION='$(SOAK_TIME)' \
		$(GO) test -race -count=1 -timeout='$(SOAK_TIMEOUT)' \
			-run '^TestSoak' ./tests/perf/...
soak: soaktest

.PHONY: fuzz
fuzz: ## Fuzz a selected Go target (FUZZ_PACKAGE, FUZZ_TARGET, FUZZ_TIME, FUZZ_PARALLEL).
	$(GO) test -parallel=$(FUZZ_PARALLEL) -run '^$$' -fuzz '^$(FUZZ_TARGET)$$' \
		-fuzztime=$(FUZZ_TIME) $(FUZZ_PACKAGE)

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
	@if git --no-pager grep -nI -E '[[:blank:]]+$$' -- '*.md'; then \
		echo 'Markdown files contain trailing whitespace.' >&2; \
		exit 1; \
	fi
	@test -s $(MANPAGE_DIR)/$(PROJECT).1
	@test -s $(COMPLETIONS_DIR)/bash/$(PROJECT)
	@test -s $(COMPLETIONS_DIR)/zsh/_$(PROJECT)
	@test -s $(COMPLETIONS_DIR)/powershell/$(PROJECT).ps1

.PHONY: spellcheck
spellcheck: ## Spell-check the repository using cspell or its pinned container.
	@if command -v cspell >/dev/null 2>&1; then \
		cspell lint --dot .; \
	elif command -v npx >/dev/null 2>&1; then \
		npx --yes cspell@$(CSPELL_VERSION) lint --dot .; \
	elif $(DOCKER) info >/dev/null 2>&1; then \
		$(DOCKER) build --progress=$(DOCKER_PROGRESS) \
			--file Dockerfile.cspell \
			--build-arg CSPELL_VERSION=$(CSPELL_VERSION) \
			--output type=cacheonly .; \
	else \
		echo 'cspell requires cspell, npx, or a running Docker daemon.' >&2; \
		exit 2; \
	fi

.PHONY: docs-site-check
docs-site-check: ## Build the GitHub Pages site with its production builder.
	$(DOCKER) build --progress=$(DOCKER_PROGRESS) \
		--file Dockerfile.pages --output type=cacheonly .

.PHONY: docs
docs: gen-all docs-check spellcheck docs-site-check ## Generate and validate documentation.

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
	$(DOCKER) build --progress=$(DOCKER_PROGRESS) --check .

.PHONY: docker-image
docker-image: ## Build the local container image.
	$(DOCKER) build --progress=$(DOCKER_PROGRESS) \
		--build-arg VERSION='$(VERSION)' \
		--build-arg VCS_REF='$(COMMIT)' \
		--build-arg BUILD_DATE='$(BUILD_DATE)' \
		--tag $(IMAGE) \
		.

.PHONY: docker-smoke
docker-smoke: docker-image ## Verify foreground/wait, persistent state, and derived-image container contracts.
	@set -eu; \
	volume="jobman-smoke-$$$$"; \
	derived='$(PROJECT)-derived-smoke:local'; \
	trap '$(DOCKER) volume rm -f "$$volume" >/dev/null 2>&1 || true; \
		$(DOCKER) image rm -f "$$derived" >/dev/null 2>&1 || true' EXIT; \
	$(DOCKER) volume create "$$volume" >/dev/null; \
	$(DOCKER) build --progress=$(DOCKER_PROGRESS) \
		--build-arg BASE_IMAGE='$(IMAGE)' --tag "$$derived" tests/container; \
	$(DOCKER) run --rm --volume "$$volume:/home/jobman/.local/state/jobman" \
		"$$derived" run --wait -- /opt/jobman/bin/container-target; \
	$(DOCKER) run --rm --volume "$$volume:/home/jobman/.local/state/jobman" \
		"$$derived" list --completed --limit 1

.PHONY: docker-run
docker-run: docker-image ## Run the local image; pass jobman arguments with ARGS='...'.
	$(DOCKER) run --rm $(DOCKER_RUN_FLAGS) $(IMAGE) $(ARGS)

.PHONY: build-all
build-all: build docker-image ## Build the local binary and container image.

.PHONY: release-check
release-check: tool-goreleaser ## Validate the GoReleaser configuration.
	$(GORELEASER) check

.PHONY: release-build
release-build: tool-goreleaser ## Compile every target declared in GoReleaser.
	$(GORELEASER) build --snapshot --clean

.PHONY: snapshot
snapshot: tool-goreleaser tool-syft ## Build a local release snapshot without publishing.
	PATH='$(abspath $(BIN_DIR))':$$PATH \
		$(GORELEASER) release --snapshot --clean --parallelism 2 \
			--skip=sign,homebrew

.PHONY: check quick-check ci
check: mod-check format-check lint workflow-check shellcheck vulncheck test docs build release-check release-build ## Run all presubmission checks.
quick-check: mod-check format-check lint unittest build ## Run the fast presubmission checks.
ci: check ## Alias for the complete CI verification workflow.

.PHONY: update
update: ## Run repository maintenance scripts.
	@set -eu; \
	export GO_VERS='$(GO_VERSION)'; \
	for script in $(sort $(wildcard $(UPDATE_SCRIPTS)/*.sh)); do \
		echo "Running $$script"; \
		"$$script"; \
	done

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
	$(RM) $(COVERAGE_FILE) $(COVERAGE_RAW) $(COVERAGE_HTML)

.PHONY: clean-tools
clean-tools: ## Remove tools installed into bin/ by this Makefile.
	$(RM) $(BIN_DIR)/golangci-lint $(BIN_DIR)/goreleaser
	$(RM) $(BIN_DIR)/actionlint $(BIN_DIR)/govulncheck
	$(RM) $(BIN_DIR)/syft
	$(RM) $(BIN_DIR)/.syft-*
