.PHONY: help fmt fmt-check vet lint lint-fix sec vuln security modcheck test build install docs docs-check release-check pipeline ci hooks tools clean

GO      ?= go
BIN     ?= tunneling
PKG     ?= .

# Where `make install` puts the binary. On PATH, and stable across rebuilds,
# which is what the LaunchAgent plist needs.
INSTALL_DIR ?= $(HOME)/bin

# Keep these in sync with .github/workflows/ci.yml so "passes locally, fails
# in CI" (and vice versa) cannot happen.
GOLANGCI_VERSION ?= v2.13.1
GOSEC_VERSION    ?= v2.28.0

help: ## Show this help.
	@grep -hE '^[a-z-]+:.*?## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

# ---- formatting ------------------------------------------------------------

fmt: ## Reformat all Go source files in place (gofumpt + goimports).
	golangci-lint fmt ./...

fmt-check: ## Fail if any Go file is not formatted.
	@golangci-lint fmt --diff ./... || { \
		echo ""; \
		echo "Some files are not formatted. Run 'make fmt' and commit the result."; \
		exit 1; \
	}

# ---- static analysis -------------------------------------------------------

vet: ## Run go vet.
	$(GO) vet ./...

lint: ## Run golangci-lint (see .golangci.yml for the enabled linters).
	@command -v golangci-lint >/dev/null 2>&1 || { \
		echo "golangci-lint not found. Install: make tools"; \
		exit 1; \
	}
	golangci-lint run ./...

lint-fix: ## Run golangci-lint and auto-fix what it can.
	golangci-lint run --fix ./...

# ---- security --------------------------------------------------------------

sec: ## Run gosec (code-level security static analysis).
	@command -v gosec >/dev/null 2>&1 || { \
		echo "gosec not found. Install: make tools"; \
		exit 1; \
	}
	gosec -quiet ./...

vuln: ## Run govulncheck (known CVEs reachable from this code).
	@command -v govulncheck >/dev/null 2>&1 || { \
		echo "govulncheck not found. Install: make tools"; \
		exit 1; \
	}
	govulncheck ./...

security: sec vuln ## Both security checks: gosec (code) + govulncheck (dependencies).

# ---- dependencies ----------------------------------------------------------

modcheck: ## Verify module checksums, and fail if go.mod/go.sum are not tidy.
	$(GO) mod verify
	@tmp=$$(mktemp -d); \
	cp go.mod "$$tmp/go.mod.orig"; \
	[ -f go.sum ] && cp go.sum "$$tmp/go.sum.orig" || true; \
	$(GO) mod tidy; \
	status=0; \
	diff -u "$$tmp/go.mod.orig" go.mod || status=1; \
	if [ -f "$$tmp/go.sum.orig" ]; then diff -u "$$tmp/go.sum.orig" go.sum || status=1; fi; \
	mv "$$tmp/go.mod.orig" go.mod; \
	if [ -f "$$tmp/go.sum.orig" ]; then mv "$$tmp/go.sum.orig" go.sum; else rm -f go.sum; fi; \
	rm -rf "$$tmp"; \
	if [ $$status -ne 0 ]; then \
		echo "go.mod/go.sum are not tidy. Run 'go mod tidy' and commit the result."; \
		exit 1; \
	fi

# ---- tests / build ---------------------------------------------------------

test: ## Run the test suite with the race detector and coverage.
	$(GO) test ./... -race -cover

build: ## Build the tunneling binary.
	$(GO) build -o $(BIN) $(PKG)

# Until the Homebrew cask exists, this is how the binary gets somewhere the
# LaunchAgent plist can point at. Not the Caskroom-versioned path a cask would
# use - see `service install` on why that path must stay stable.
install: build ## Install the binary to $(INSTALL_DIR).
	@mkdir -p $(INSTALL_DIR)
	install -m 0755 $(BIN) $(INSTALL_DIR)/$(BIN)
	@echo "installed $(INSTALL_DIR)/$(BIN)"
	@echo "if the LaunchAgent is loaded, run: $(BIN) service restart"

# ---- docs / release --------------------------------------------------------

docs: ## Regenerate docs/ from the cobra command tree.
	$(GO) run ./tools/gendocs

docs-check: ## Fail if docs/ is out of date with the command tree.
	@$(GO) run ./tools/gendocs >/dev/null
	@if ! git diff --quiet -- docs; then \
		echo "docs/ is out of date. Run 'make docs' and commit the result:"; \
		git --no-pager diff --stat -- docs; \
		exit 1; \
	fi

release-check: ## Validate .goreleaser.yaml and build a full release locally, without publishing.
	@command -v goreleaser >/dev/null 2>&1 || { \
		echo "goreleaser not found. Install: brew install goreleaser"; \
		exit 1; \
	}
	goreleaser check
	goreleaser release --snapshot --clean

# ---- aggregate targets -----------------------------------------------------

pipeline: fmt-check vet lint security modcheck docs-check test build ## The full local pipeline - run this before pushing.
	@echo ""
	@echo "pipeline: all checks passed."

ci: pipeline ## Alias for `pipeline`; CI runs the same steps, split across jobs.

# ---- housekeeping ----------------------------------------------------------

tools: ## Install/update the external tools the pipeline needs.
	# golangci-lint recommends its install script over `go install` (see
	# https://golangci-lint.run/docs/welcome/install/local/) - it ships a
	# prebuilt binary instead of compiling against your local Go toolchain.
	curl -sSfL https://golangci-lint.run/install.sh | sh -s -- -b $$($(GO) env GOPATH)/bin $(GOLANGCI_VERSION)
	$(GO) install github.com/securego/gosec/v2/cmd/gosec@$(GOSEC_VERSION)
	$(GO) install golang.org/x/vuln/cmd/govulncheck@latest
	@command -v lefthook   >/dev/null 2>&1 || echo "NOTE: lefthook not found - install it (brew install lefthook) then run 'make hooks'."
	@command -v gitleaks   >/dev/null 2>&1 || echo "NOTE: gitleaks not found - install it (brew install gitleaks) for the pre-commit secret scan."
	@command -v goreleaser >/dev/null 2>&1 || echo "NOTE: goreleaser not found - install it (brew install goreleaser) for 'make release-check'."

hooks: ## Install the git hooks (pre-commit secret scan, pre-push pipeline).
	lefthook install
	@echo "Git hooks installed. Run this once per clone - it is not automatic."

clean:
	rm -rf $(BIN) dist/
