GO ?= go
WAILS ?= wails
WAILS_TAGS ?= webkit2_41
# Release version: the single source is info.productVersion in wails.json.
VERSION ?= $(shell sed -n 's/^ *"productVersion": *"\([^"]*\)".*/\1/p' wails.json)
ARCH ?= $(shell $(GO) env GOARCH)
NFPM ?= $(GO) run github.com/goreleaser/nfpm/v2/cmd/nfpm@v2.47.0

.PHONY: test lint fmt cli fuzz-smoke golden build clean dev app gui-build check-gui-deps package-linux package-macos

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
	$(GO) build -tags "$(WAILS_TAGS),production" -trimpath -ldflags "-s -w" -o build/bin/agent-sessions ./cmd/agent-sessions

# Linux .deb and .rpm packages in dist/, wrapping the gui-build binary.
# Runtime dependencies are declared in packaging/nfpm.yaml.
package-linux: gui-build
	mkdir -p dist
	VERSION=$(VERSION) ARCH=$(ARCH) $(NFPM) pkg --config packaging/nfpm.yaml --packager deb --target dist/
	VERSION=$(VERSION) ARCH=$(ARCH) $(NFPM) pkg --config packaging/nfpm.yaml --packager rpm --target dist/

# Universal macOS app wrapped in a .dmg in dist/. Runs on macOS only and
# requires the Wails CLI. The bundle is ad-hoc signed, not notarized.
# hdiutil intermittently fails with "Resource busy" on CI runners, so the
# .dmg step retries once.
package-macos:
	cd frontend && npm run build
	# Build each architecture separately. Wails' darwin/universal path disables
	# its bin cleanup and then asks lipo to chdir into build/bin before that
	# directory has been created.
	$(WAILS) build -platform darwin/arm64 -tags desktop -clean -s -m -nosyncgomod -skipbindings -trimpath
	mv build/bin/agent-sessions.app/Contents/MacOS/agent-sessions build/bin/agent-sessions-arm64
	$(WAILS) build -platform darwin/amd64 -tags desktop -clean -s -m -nosyncgomod -skipbindings -trimpath
	mv build/bin/agent-sessions.app/Contents/MacOS/agent-sessions build/bin/agent-sessions-amd64
	lipo -create -output build/bin/agent-sessions.app/Contents/MacOS/agent-sessions \
		build/bin/agent-sessions-arm64 build/bin/agent-sessions-amd64
	rm -f build/bin/agent-sessions-arm64 build/bin/agent-sessions-amd64
	rm -rf "build/bin/Agent Sessions.app" build/dmg
	mv build/bin/agent-sessions.app "build/bin/Agent Sessions.app"
	codesign --force --deep --sign - "build/bin/Agent Sessions.app"
	mkdir -p build/dmg dist
	cp -R "build/bin/Agent Sessions.app" build/dmg/
	ln -s /Applications build/dmg/Applications
	hdiutil create -volname "Agent Sessions" -srcfolder build/dmg -ov -format UDZO dist/agent-sessions_$(VERSION)_macos_universal.dmg || \
		{ sleep 5; hdiutil create -volname "Agent Sessions" -srcfolder build/dmg -ov -format UDZO dist/agent-sessions_$(VERSION)_macos_universal.dmg; }

clean:
	rm -rf bin build/bin build/dmg dist frontend/dist