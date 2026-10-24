MODULE  := github.com/AramZahedi/dev-web-stack
VERSION := $(shell git describe --tags --dirty 2>/dev/null || echo dev)
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null)
LDFLAGS := -s -w \
	-X $(MODULE)/internal/version.Version=$(VERSION) \
	-X $(MODULE)/internal/version.Commit=$(COMMIT)

PHP_LINE       ?= 8.4
PHP_REVISION   ?= 1
PHP_OUTPUT     ?= dist/php
PHP_VERSION    ?= $(shell sed -n 's/^PHP_$(subst .,_,$(PHP_LINE))_VERSION=//p' build/php/extensions.lock 2>/dev/null)
PHP_ARTIFACT   ?= $(PHP_OUTPUT)/php-$(PHP_VERSION)-r$(PHP_REVISION)-linux-x64.tar.gz
# Override with the final release asset base URL before publishing a catalog.
PHP_BASE_URL   ?= https://github.com/AramZahedi/dev-web-stack/releases/download/php-builds
PHP_SMOKE_BIN  ?=

.PHONY: build test lint ui e2e clean php-build php-smoke php-catalog php-test

build: ui
	mkdir -p dist
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o dist/devstack ./cmd/devstack
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o dist/devstack-helper ./cmd/devstack-helper

test:
	go test ./...

lint:
	golangci-lint run

ui:
	@if [ -f ui/package.json ]; then \
		cd ui && npm ci && npm run build; \
	else \
		echo "ui: no package.json yet, nothing to build"; \
	fi

e2e: build
	E2E_SKIP_BUILD=1 bash test/e2e/linux.sh

clean:
	rm -rf dist

# PHP artifacts are built and published by the php.yml CI workflow. These
# targets are for contributors who have the build dependencies from
# build/php/README.md. The production catalog stays empty until the release
# job publishes the resulting checksummed archives.
php-build:
	@test -n "$(PHP_VERSION)" || { echo "no locked PHP version for line $(PHP_LINE); run build/php/update-lock.sh" >&2; exit 1; }
	bash build/php/build.sh --line "$(PHP_LINE)" --version "$(PHP_VERSION)" --revision "$(PHP_REVISION)" --output "$(PHP_OUTPUT)"

php-smoke:
	@test -f "$(PHP_ARTIFACT)" || { echo "missing $(PHP_ARTIFACT); run make php-build" >&2; exit 1; }
	PHP_SMOKE_BIN="$(PHP_SMOKE_BIN)" bash build/php/smoke.sh "$(PHP_ARTIFACT)"

php-catalog:
	@test -f "$(PHP_ARTIFACT)" || { echo "missing $(PHP_ARTIFACT); run make php-build" >&2; exit 1; }
	@mkdir -p "$(PHP_OUTPUT)"
	@sha=$$(sha256sum "$(PHP_ARTIFACT)" | cut -d' ' -f1); \
	size=$$(stat -c '%s' "$(PHP_ARTIFACT)"); \
	go run ./cmd/phpcatalog \
		--version "$(PHP_VERSION)" \
		--revision "$(PHP_REVISION)" \
		--artifact "$(PHP_ARTIFACT)" \
		--sha256 "$$sha" \
		--size "$$size" \
		--base-url "$(PHP_BASE_URL)" \
		--output "$(PHP_OUTPUT)/php-$(PHP_VERSION)-r$(PHP_REVISION).json"

php-test:
	go test ./cmd/phpcatalog ./cmd/php-smoke
	go vet ./cmd/phpcatalog ./cmd/php-smoke
	bash -n build/php/*.sh
	@if command -v shellcheck >/dev/null 2>&1; then \
		shellcheck --severity=error build/php/*.sh; \
	else \
		echo "php-test: shellcheck not installed, skipped"; \
	fi
