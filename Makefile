MODULE  := github.com/AramZahedi/dev-web-stack
VERSION := $(shell git describe --tags --dirty 2>/dev/null || echo dev)
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null)
LDFLAGS := -s -w \
	-X $(MODULE)/internal/version.Version=$(VERSION) \
	-X $(MODULE)/internal/version.Commit=$(COMMIT)

.PHONY: build test lint ui clean

build:
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

clean:
	rm -rf dist
