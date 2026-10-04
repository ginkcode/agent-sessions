GO ?= go
WAILS ?= wails
WAILS_TAGS ?= webkit2_41
# Release version: the single source is info.productVersion in wails.json.
VERSION ?= $(shell sed -n 's/^ *"productVersion": *"\([^"]*\)".*/\1/p' wails.json)
VERSION_PKG ?= github.com/ginkcode/agent-sessions/internal/version
LDFLAGS ?= -s -w -X $(VERSION_PKG).Version=$(VERSION)
ARCH ?= $(shell $(GO) env GOARCH)
NFPM ?= $(GO) run github.com/goreleaser/nfpm/v2/cmd/nfpm@v2.47.0
# One icon source for every platform. Wails reads build/appicon.png for the
# macOS icns and the Windows ico.
APPICON := frontend/assets/Icon-universal.png

.PHONY: test test-ssh lint fmt cli fuzz-smoke golden build clean dev app gui-build check-gui-deps package-linux package-macos package-windows \
	remote-servers version tags set-version tag untag release help check-app-icons app-icon

# Headless servers the desktop app deploys over SSH. CGO stays off: these
# binaries have no GUI. Cross targets need a Go toolchain that can produce
# them (CI does); `make remote-servers HOST=1` builds only the host pair.
# packaging/windows/agent-sessions.wxs lists one file per platform.
REMOTE_PLATFORMS ?= linux/amd64 linux/arm64 darwin/amd64 darwin/arm64
REMOTE_DIR ?= build/remote

test:
	$(GO) test -race ./...

# Opt-in SSH integration tests against a throwaway Docker sshd
# (internal/remote/sshtest). Needs docker, ssh and ssh-keygen.
test-ssh:
	AGENT_SESSIONS_SSH_DOCKER=1 $(GO) test -race -count=1 -run 'SSHD' ./internal/remote/... ./internal/app/...

lint:
	$(GO) tool golangci-lint run

fmt:
	$(GO) tool gofumpt -w .
	$(GO) tool golangci-lint fmt

cli:
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/agent-sessions-cli ./cmd/agent-sessions-cli

# Gzipped headless servers in $(REMOTE_DIR), plus SHA256SUMS of those archives.
# The checksum covers the .gz bytes: the remote deploy verifies that file.
remote-servers:
	mkdir -p "$(REMOTE_DIR)"
	rm -f "$(REMOTE_DIR)"/SHA256SUMS "$(REMOTE_DIR)"/*.gz
	@set -e; \
	platforms='$(REMOTE_PLATFORMS)'; \
	if [ "$(HOST)" = 1 ]; then platforms="$$($(GO) env GOOS)/$$($(GO) env GOARCH)"; fi; \
	for pair in $$platforms; do \
		os=$${pair%/*}; arch=$${pair#*/}; \
		name="agent-sessions-cli-$$os-$$arch"; \
		echo "building $$name"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch \
			$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o "$(REMOTE_DIR)/$$name" ./cmd/agent-sessions-cli; \
		gzip -c -n "$(REMOTE_DIR)/$$name" > "$(REMOTE_DIR)/$$name.gz"; \
		rm -f "$(REMOTE_DIR)/$$name"; \
	done; \
	if command -v sha256sum >/dev/null 2>&1; then \
		(cd "$(REMOTE_DIR)" && sha256sum -- *.gz > SHA256SUMS); \
	else \
		(cd "$(REMOTE_DIR)" && shasum -a 256 -- *.gz > SHA256SUMS); \
	fi; \
	echo "remote servers in $(REMOTE_DIR):"; \
	cat "$(REMOTE_DIR)/SHA256SUMS"

fuzz-smoke:
	$(GO) test ./internal/provider/claude -run='^$$' -fuzz=FuzzRecord -fuzztime=60s

golden:
	$(GO) test ./... -update

build: cli

# Linux desktop build requires a C compiler, libgtk-3-dev and
# libwebkit2gtk-4.1-dev.
# On Ubuntu: sudo apt install gcc libgtk-3-dev libwebkit2gtk-4.1-dev
# This target checks them before invoking Wails, with an actionable hint.
# Without a C compiler Go silently turns cgo off, and the Wails Linux
# frontend then fails with "undefined: Frontend".
check-gui-deps:
	@[ "$$($(GO) env CGO_ENABLED)" = 1 ] || { \
		printf '%s\n' 'cgo is off: the Linux desktop build needs a C compiler. Install gcc (or set CC).' >&2; \
		exit 1; \
	}
	@pkg-config --exists webkit2gtk-4.1 gtk+-3.0 || { \
		printf '%s\n' 'Missing WebKitGTK 4.1/GTK 3 development headers. Install libgtk-3-dev libwebkit2gtk-4.1-dev.' >&2; \
		exit 1; \
	}

# Run the Svelte/Vite dev server and the Wails desktop window.
# The Wails v2 CLI is required: go install github.com/wailsapp/wails/v2/cmd/wails@v2.14.0
# NOTE: do not launch in headless CI; use `make gui-build` for compile checks.
dev: check-gui-deps app-icon
	# Stamp the same version the deployed server reports, or the handshake
	# rejects it (a plain `wails dev` would be version "dev").
	$(WAILS) dev -tags $(WAILS_TAGS) -ldflags "$(LDFLAGS)"

# Compile a standalone desktop binary at build/bin/agent-sessions.
app: check-gui-deps app-icon
	$(WAILS) build -tags $(WAILS_TAGS) -clean -ldflags "$(LDFLAGS)"

# Headless equivalent of the GUI compile (without invoking Wails CLI):
# npm run build must run first so frontend/dist exists for go:embed.
# The production tag is required at runtime: without it Wails' internal
# app stub returns "will not build without the correct build tags".
gui-build: check-gui-deps
	cd frontend && npm run check && npm run build
	$(GO) build -tags "$(WAILS_TAGS),production" -trimpath -ldflags "$(LDFLAGS)" -o build/bin/agent-sessions ./cmd/agent-sessions

# Verify the icon source before packaging it. It stays 1024px so Wails can
# generate every macOS/Windows representation from it.
check-app-icons:
	@case "$$(file -b "$(APPICON)")" in \
		'PNG image data, 1024 x 1024, '*'RGBA'*) ;; \
		*) echo "$(APPICON) must be a 1024x1024 RGBA PNG" >&2; exit 1 ;; \
	esac

# Stage the icon where Wails looks for it.
app-icon: check-app-icons
	mkdir -p build
	cp "$(APPICON)" build/appicon.png

# Linux .deb and .rpm packages in dist/, wrapping the gui-build binary.
# Runtime dependencies are declared in packaging/nfpm.yaml. The package ships
# the headless server for every remote platform, since a Linux desktop can
# connect to a macOS host.
package-linux: check-app-icons gui-build
	$(MAKE) remote-servers
	mkdir -p dist
	VERSION=$(VERSION) ARCH=$(ARCH) $(NFPM) pkg --config packaging/nfpm.yaml --packager deb --target dist/
	VERSION=$(VERSION) ARCH=$(ARCH) $(NFPM) pkg --config packaging/nfpm.yaml --packager rpm --target dist/

# Universal macOS app wrapped in a .dmg in dist/. Runs on macOS only and
# requires the Wails CLI. The bundle is ad-hoc signed, not notarized.
# hdiutil intermittently fails with "Resource busy" on CI runners, so the
# .dmg step retries once.
package-macos: check-app-icons
	cd frontend && npm run build
	# wails.json keeps projectdir relative so the checked-in file is portable,
	# but Wails resolves its relative -o path from projectdir and then checks
	# that same path from the repository root. An absolute projectdir makes
	# both operations use one path.
	@set -e; \
	root=$$(pwd); \
	backup=$$(mktemp ./wails.json.release.XXXXXX); \
	cp -p wails.json "$$backup"; \
	mkdir -p build; \
	cp "$(APPICON)" build/appicon.png; \
	trap 'mv "$$backup" wails.json' EXIT; \
	jq --arg root "$$root" '.projectdir = ($$root + "/cmd/agent-sessions") | .["build:dir"] = ($$root + "/build")' \
		"$$backup" > wails.json; \
	$(WAILS) build -platform darwin/arm64 -tags desktop -clean -s -m -nosyncgomod -skipbindings -trimpath -ldflags "$(LDFLAGS)"; \
	mv build/bin/agent-sessions.app/Contents/MacOS/agent-sessions build/agent-sessions-arm64; \
	$(WAILS) build -platform darwin/amd64 -tags desktop -clean -s -m -nosyncgomod -skipbindings -trimpath -ldflags "$(LDFLAGS)"; \
	mv build/bin/agent-sessions.app/Contents/MacOS/agent-sessions build/agent-sessions-amd64
	lipo -create -output build/bin/agent-sessions.app/Contents/MacOS/agent-sessions \
		build/agent-sessions-arm64 build/agent-sessions-amd64
	rm -f build/agent-sessions-arm64 build/agent-sessions-amd64
	rm -rf "build/bin/Agent Sessions.app" build/dmg
	mv build/bin/agent-sessions.app "build/bin/Agent Sessions.app"
	# Servers go in before codesign so the signature covers them.
	$(MAKE) remote-servers REMOTE_DIR="build/bin/Agent Sessions.app/Contents/Resources/remote"
	codesign --force --deep --sign - "build/bin/Agent Sessions.app"
	mkdir -p build/dmg dist
	cp -R "build/bin/Agent Sessions.app" build/dmg/
	ln -s /Applications build/dmg/Applications
	hdiutil create -volname "Agent Sessions" -srcfolder build/dmg -ov -format UDZO dist/agent-sessions_$(VERSION)_macos_universal.dmg || \
		{ sleep 5; hdiutil create -volname "Agent Sessions" -srcfolder build/dmg -ov -format UDZO dist/agent-sessions_$(VERSION)_macos_universal.dmg; }

# Windows amd64 .msi in dist/, cross-compiled on Linux. Requires the Wails
# CLI, jq and wixl (apt install wixl, 0.102 or later for
# AllowSameVersionUpgrades). The package is described in
# packaging/windows/agent-sessions.wxs; build/windows is regenerated each run
# so the icon and version resources follow the current sources. The package
# is not code-signed, so SmartScreen warns on first run.
package-windows: check-app-icons
	@for tool in jq wixl; do \
		command -v $$tool >/dev/null 2>&1 || { echo "$$tool not found: install it (apt install $$tool)" >&2; exit 1; }; \
	done
	cd frontend && npm run build
	$(MAKE) remote-servers
	# Same absolute projectdir as package-macos. The version is pinned to
	# VERSION so the exe and the package agree.
	@set -e; \
	root=$$(pwd); \
	backup=$$(mktemp ./wails.json.release.XXXXXX); \
	cp -p wails.json "$$backup"; \
	rm -rf build/windows; \
	mkdir -p build; \
	cp "$(APPICON)" build/appicon.png; \
	trap 'mv "$$backup" wails.json' EXIT; \
	jq --arg root "$$root" --arg version '$(VERSION)' \
		'.projectdir = ($$root + "/cmd/agent-sessions") | .["build:dir"] = ($$root + "/build") | .info.productVersion = $$version' \
		"$$backup" > wails.json; \
	$(WAILS) build -platform windows/amd64 -tags desktop -clean -s -m -nosyncgomod -skipbindings -trimpath -ldflags "$(LDFLAGS)"
	mkdir -p dist
	@set -e; \
	root=$$(pwd); \
	version='$(VERSION)'; \
	set -x; \
	wixl -a x64 -o dist/agent-sessions_$(VERSION)_windows_amd64.msi \
		-D Version="$${version%%-*}" -D BinDir="$$root/build/bin" -D RemoteDir="$$root/build/remote" \
		-D IconFile="$$root/build/windows/icon.ico" packaging/windows/agent-sessions.wxs

# Release tags (see make help). Pushing v<VERSION> runs
# .github/workflows/release.yml, which refuses a tag that does not match
# wails.json.
TAG := v$(VERSION)
SEMVER_RE := ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$$

version:
	@echo "wails.json version: $(VERSION)"
	@echo "latest tag:         $$(git describe --tags --abbrev=0 --match 'v*' 2>/dev/null || echo none)"
	@if git rev-parse -q --verify "refs/tags/$(TAG)" >/dev/null; then \
		echo "local tag:          $(TAG) at $$(git rev-list -n1 --abbrev-commit $(TAG)) (HEAD is $$(git rev-parse --short HEAD))"; \
	else echo "local tag:          $(TAG) not created"; fi
	@if git ls-remote --exit-code --tags origin "refs/tags/$(TAG)" >/dev/null 2>&1; then \
		echo "origin tag:         $(TAG) pushed"; \
	else echo "origin tag:         $(TAG) not pushed"; fi

tags:
	@git tag -l 'v*' --sort=-v:refname \
		--format='%(refname:short)%09%(if)%(*objectname)%(then)%(*objectname:short)%(else)%(objectname:short)%(end)%09%(creatordate:short)%09%(subject)'

set-version:
	@test -n "$(V)" || { echo 'usage: make set-version V=x.y.z' >&2; exit 1; }
	@echo "$(V)" | grep -Eq '$(SEMVER_RE)' || { echo "$(V) is not a semantic version" >&2; exit 1; }
	@! git rev-parse -q --verify "refs/tags/v$(V)" >/dev/null || { echo "v$(V) is already tagged" >&2; exit 1; }
	@git diff --quiet HEAD -- || { echo 'commit or stash your changes first' >&2; exit 1; }
	sed -i.bak 's/^\( *"productVersion": *"\)[^"]*"/\1$(V)"/' wails.json && rm -f wails.json.bak
	git commit -m "chore: release v$(V)" -- wails.json

tag:
	@echo "$(VERSION)" | grep -Eq '$(SEMVER_RE)' || { echo "wails.json version '$(VERSION)' is not a semantic version" >&2; exit 1; }
	@git diff --quiet HEAD -- || { echo 'commit or stash your changes before tagging' >&2; exit 1; }
	@! git rev-parse -q --verify "refs/tags/$(TAG)" >/dev/null || { echo "$(TAG) already exists; bump with make set-version V=..." >&2; exit 1; }
	git tag -a "$(TAG)" -m "Agent Sessions $(VERSION)"
	@echo "Created $(TAG) at $$(git rev-parse --short HEAD). Publish it with: make release"

untag:
	git tag -d "$(TAG)"
	@! git ls-remote --exit-code --tags origin "refs/tags/$(TAG)" >/dev/null 2>&1 || \
		echo "$(TAG) is still on origin; releases are not withdrawn automatically."

release:
	@git rev-parse -q --verify "refs/tags/$(TAG)" >/dev/null || $(MAKE) --no-print-directory tag
	git push origin "$(TAG)"

help:
	@printf 'Development\n'
	@printf '  %-22s %s\n' 'test' 'Run Go tests with the race detector'
	@printf '  %-22s %s\n' 'test-ssh' 'Run the SSH integration tests against a Docker sshd'
	@printf '  %-22s %s\n' 'lint' 'Run golangci-lint'
	@printf '  %-22s %s\n' 'fmt' 'Format Go code'
	@printf '  %-22s %s\n' 'cli / build' 'Build bin/agent-sessions-cli'
	@printf '  %-22s %s\n' 'remote-servers' 'Build gzipped headless servers (HOST=1: this machine only)'
	@printf '  %-22s %s\n' 'fuzz-smoke' 'Fuzz the Claude record parser for 60s'
	@printf '  %-22s %s\n' 'golden' 'Regenerate golden test files'
	@printf '  %-22s %s\n' 'dev' 'Run the desktop app with live reload (Wails CLI)'
	@printf '  %-22s %s\n' 'app' 'Build the desktop app with the Wails CLI'
	@printf '  %-22s %s\n' 'gui-build' 'Build build/bin/agent-sessions without the Wails CLI'
	@printf '  %-22s %s\n' 'clean' 'Remove build outputs'
	@printf '\nPackaging (writes dist/)\n'
	@printf '  %-22s %s\n' 'package-linux' '.deb and .rpm for ARCH (default: host)'
	@printf '  %-22s %s\n' 'package-macos' 'Universal .dmg (macOS only)'
	@printf '  %-22s %s\n' 'package-windows' 'amd64 .msi (needs wixl, jq)'
	@printf '\nReleases (current: v$(VERSION))\n'
	@printf '  %-22s %s\n' 'version' 'Show the version and whether its tag exists'
	@printf '  %-22s %s\n' 'tags' 'List release tags, newest first'
	@printf '  %-22s %s\n' 'set-version V=x.y.z' 'Bump wails.json and commit it'
	@printf '  %-22s %s\n' 'tag' 'Create tag v<version> at HEAD (local only)'
	@printf '  %-22s %s\n' 'untag' 'Delete the local tag'
	@printf '  %-22s %s\n' 'release' 'Create the tag if needed and push it; CI publishes'

clean:
	rm -rf bin build/bin build/dmg build/remote dist frontend/dist