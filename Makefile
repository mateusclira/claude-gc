BINARY  := claude-gc
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

.PHONY: build test lint vet fmt install snapshot clean

build: ## Build bin/claude-gc
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o bin/$(BINARY) ./cmd/$(BINARY)

test: ## Run the tests with the race detector
	go test -race -count=1 ./...

vet: ## Run go vet
	go vet ./...

lint: ## Run golangci-lint
	golangci-lint run

fmt: ## Format the code
	gofmt -s -w .

install: ## Install into $(GOPATH)/bin
	CGO_ENABLED=0 go install -trimpath -ldflags '$(LDFLAGS)' ./cmd/$(BINARY)

snapshot: ## Build release artifacts locally without publishing
	goreleaser release --snapshot --clean --skip=publish

clean:
	rm -rf bin dist

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-10s %s\n", $$1, $$2}'
