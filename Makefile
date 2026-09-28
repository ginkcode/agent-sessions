GO ?= go
WAILS ?= wails
WAILS_TAGS ?= webkit2_41

.PHONY: test lint fmt cli fuzz-smoke golden build clean dev app gui-build check-gui-deps

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

# Linux desktop build requires libgtk-3-dev and libwebkit2gtk-4.1-dev.
# On Ubuntu: sudo apt install libgtk-3-dev libwebkit2gtk-4.1-dev
# This target checks headers before invoking Wails, with an actionable hint.
check-gui-deps:
	@pkg-config --exists webkit2gtk-4.1 gtk+-3.0 || { \
		printf '%s\n' 'Missing WebKitGTK 4.1/GTK 3 development headers. Install libgtk-3-dev libwebkit2gtk-4.1-dev.' >&2; \
		exit 1; \
	}

# Run the Svelte/Vite dev server and the Wails desktop window.
# The Wails v2 CLI is required: go install github.com/wailsapp/wails/v2/cmd/wails@v2.14.0
# NOTE: do not launch in headless CI; use `make gui-build` for compile checks.
dev: check-gui-deps
	$(WAILS) dev -tags $(WAILS_TAGS)

# Compile a standalone desktop binary at build/bin/agent-sessions.
app: check-gui-deps
	$(WAILS) build -tags $(WAILS_TAGS) -clean

# Headless equivalent of the GUI compile (without invoking Wails CLI):
# npm run build must run first so frontend/dist exists for go:embed.
# The production tag is required at runtime: without it Wails' internal
# app stub returns "will not build without the correct build tags".
gui-build: check-gui-deps
	cd frontend && npm run check && npm run build
	$(GO) build -tags "$(WAILS_TAGS),production" -o build/bin/agent-sessions ./cmd/agent-sessions

clean:
	rm -rf bin build/bin frontend/dist