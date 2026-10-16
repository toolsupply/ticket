# `ticket` build entry point.
#
# Go's canonical build command is `go build`; Go has no mandated build
# system. This Makefile is a thin, dependency-free wrapper that (a) pins the
# exact commands the project relies on and (b) keeps cross-compiled outputs in
# dist/ with per-target names, so a cross-compile can never overwrite the local
# host binary (the footgun that left a darwin/arm64 file named `ticket` in the
# repo root). The commands document the local checks used by the project.

GO     ?= go
PKG   := ./cmd/ticket
RACE_PKGS ?= ./...
BIN   := bin
DIST  := dist
LDFLAGS ?= -s -w
VERSION := $(strip $(shell cat VERSION 2>/dev/null))
SKILL := skills/ticket-tasks
GOUTILS_DIR ?= ../goutils
GOUTILS_REV ?= 1e0703d171b58dd89777e9e2fcc3cc84ae54115b
# "GOOS/GOARCH" release targets (cross-compile with CGO disabled).
# Windows arm64 is intentionally left out of the first release matrix; it can
# be added without changing the packaging layout when there is demand for it.
TARGETS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64
ARCHIVES := $(DIST)/release

ifeq ($(VERSION),)
$(error VERSION must contain a release version)
endif

.PHONY: build dist archives release test test-goutils-migration race race-fresh vet fmt clean

# Host binary for THIS machine -> bin/ticket (matches the Linux dev host).
build:
	@mkdir -p $(BIN)
	CGO_ENABLED=0 $(GO) build -ldflags "$(LDFLAGS) -X github.com/toolsupply/ticket/internal/cli.Version=$(VERSION)" -o $(BIN)/ticket $(PKG)

# All release targets -> dist/ticket-<os>-<arch>[.exe].
dist:
	@mkdir -p $(DIST)
	@for t in $(TARGETS); do \
		os=$${t%/*}; arch=$${t#*/}; \
		out=$(DIST)/ticket-$${os}-$${arch}; \
		case $$os in windows) out=$${out}.exe;; esac; \
		echo "build $$os/$$arch -> $$out"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $(GO) build -ldflags "$(LDFLAGS) -X github.com/toolsupply/ticket/internal/cli.Version=$(VERSION)" -o "$$out" $(PKG) || exit 1; \
	done

# Package dist binaries in the same shape used by GitHub releases. Each
# archive contains only the executable, named ticket or ticket.exe.
archives: dist
	@rm -rf $(ARCHIVES)
	@mkdir -p $(ARCHIVES)
	@for t in $(TARGETS); do \
		os=$${t%/*}; arch=$${t#*/}; \
		case $$os in \
			windows) \
				stage="$(ARCHIVES)/.stage-$$os-$$arch"; mkdir -p "$$stage"; \
				cp "$(DIST)/ticket-$$os-$$arch.exe" "$$stage/ticket.exe"; \
				zip -q -j "$(ARCHIVES)/ticket-$$os-$$arch.zip" "$$stage/ticket.exe"; \
				rm -rf "$$stage";; \
			*) \
				stage="$(ARCHIVES)/.stage-$$os-$$arch"; mkdir -p "$$stage"; \
				cp "$(DIST)/ticket-$$os-$$arch" "$$stage/ticket"; \
				tar -C "$$stage" -czf "$(ARCHIVES)/ticket-$$os-$$arch.tar.gz" ticket; \
				rm -rf "$$stage";; \
		esac || exit 1; \
	done
	@stage="$(ARCHIVES)/.stage-ticket-tasks"; mkdir -p "$$stage"; cp -R "$(SKILL)/." "$$stage/ticket-tasks"; (cd "$$stage" && zip -q -r ../ticket-tasks.zip ticket-tasks); rm -rf "$$stage"
	@cd $(ARCHIVES) && sha256sum *.tar.gz *.zip > SHA256SUMS

release: archives

test:
	$(GO) test ./... -count=1

race:
	$(GO) test -race $(RACE_PKGS)

# Run the complete race suite without using cached test results.
race-fresh:
	$(GO) test -race -count=1 ./...

vet:
	$(GO) vet ./...

fmt:
	@bad=$$(gofmt -l .); if [ -n "$$bad" ]; then echo "gofmt needed:"; echo "$$bad"; exit 1; fi; echo "gofmt clean"

clean:
	rm -rf $(BIN) $(DIST)
	rm -f ticket ticket.exe

# Opt-in schema-fit checks against the exact goutils revision. Keep this
# migration-only dependency out of Ticket's default module and CI graph.
test-goutils-migration:
	@set -eu; \
	  goutils_dir="$(GOUTILS_DIR)"; \
	  test -f "$$goutils_dir/cli/go.mod" || { echo "GOUTILS_DIR must point to the goutils repository" >&2; exit 1; }; \
	  git -C "$$goutils_dir" cat-file -e "$(GOUTILS_REV)^{commit}"; \
	  tmpdir=$$(mktemp -d "$${TMPDIR:-/tmp}/ticket-goutils-migration.XXXXXX"); \
	  trap 'rm -rf "$$tmpdir"' EXIT HUP INT TERM; \
	  mkdir -p "$$tmpdir/goutils"; \
	  git -C "$$goutils_dir" archive "$(GOUTILS_REV)" | tar -x -C "$$tmpdir/goutils"; \
	  cp go.mod "$$tmpdir/ticket.mod"; \
	  printf '\nrequire github.com/toolsupply/goutils/cli v0.0.0-20261003183322-1e0703d171b5\nreplace github.com/toolsupply/goutils/cli => %s\n' "$$tmpdir/goutils/cli" >> "$$tmpdir/ticket.mod"; \
	  sed '1c\//go:build goutilsmigration' internal/cli/goutils_schema_migration_test.go > "$$tmpdir/goutils_schema_migration_test.go"; \
	  printf '{"Replace":{"%s/internal/cli/goutils_schema_migration_test.go":"%s/goutils_schema_migration_test.go"}}\n' "$(CURDIR)" "$$tmpdir" > "$$tmpdir/overlay.json"; \
	  GOTOOLCHAIN=local GOPROXY=off $(GO) test -tags=goutilsmigration -overlay="$$tmpdir/overlay.json" -count=1 -mod=mod -modfile="$$tmpdir/ticket.mod" ./internal/cli -run '^TestGoutilsMigrationCandidate'
