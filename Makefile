GO ?= go

.PHONY: test lint fmt cli fuzz-smoke golden build clean

test:
	$(GO) test -race ./...

lint:
	$(GO) tool golangci-lint run

fmt:
	$(GO) tool gofumpt -w .
	$(GO) tool golangci-lint fmt

cli:
	$(GO) build -o bin/agent-sessions-cli ./cmd/agent-sessions-cli

fuzz-smoke:
	$(GO) test ./internal/provider/claude -run='^$$' -fuzz=FuzzRecord -fuzztime=60s

golden:
	$(GO) test ./... -update

build: cli

clean:
	rm -rf bin