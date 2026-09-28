# M1 — GUI: Wails v2 Desktop Shell & Svelte 5 Frontend: detailed plan

Covers tasks M1-15 … M1-28 from [TASKS.md](../../TASKS.md). This document defines the
concrete implementation plan for the graphical desktop interface of `agent-sessions`.
It builds on the authoritative contracts and data models specified in
[docs/plan/M0.md](M0.md) and [SPEC.md](../../SPEC.md).

---

## 1. Architecture Overview & Decisions

### 1.1 Boundary Architecture

The desktop GUI enforces strict boundary isolation:
1. **Wails containment:** Only `cmd/agent-sessions` and `internal/app` import Wails v2 (`github.com/wailsapp/wails/v2`). Providers, models, index, and grouping engines remain decoupled from any GUI framework.
2. **Frontend abstraction layer:** UI components never import `frontend/wailsjs/*`. All backend calls and runtime events route exclusively through `frontend/src/lib/api.ts`.
3. **Dual runtime mode (Mock vs Live):** When `window.go` is undefined (in plain browsers via `npm run dev` or headless Vitest/Playwright tests), `api.ts` falls back to an in-memory mock implementation (`MockBackendAPI`) serving static fixtures.

```
┌────────────────────────────────────────────────────────────────────────┐
│                          Go Backend Process                            │
│  cmd/agent-sessions/main.go ──> internal/app/App (Wails Bound Service)  │
│                                      │                                 │
│                   ┌──────────────────┼─────────────────┐               │
│                   ▼                  ▼                 ▼               │
│            internal/scan      internal/group    internal/provider      │
│              (Catalog)        (Grouping Tree)    (Claude/OpenCode/...) │
└───────────────────┬────────────────────────────────────▲───────────────┘
                    │ Wails IPC / Events                 │
                    ▼                                    │
┌───────────────────┴────────────────────────────────────┴───────────────┐
│                      WebKitGTK 4.1 WebView (Linux)                     │
│  frontend/wailsjs/go/app/App.js <── Auto-generated Wails Bindings      │
│                       ▲                                                │
│                       │ wrapped by                                     │
│  frontend/src/lib/api.ts (BackendAPI Interface)                        │
│          ▲                              ▲                              │
│          │ live IPC                     │ mock fixtures                │
│    Wails Runtime                 MockBackendAPI                        │
│          ▲                              ▲                              │
│          └──────────────┬───────────────┘                              │
│                         ▼                                              │
│          frontend/src/lib/stores/appState.svelte.ts                    │
│                         ▼                                              │
│               Svelte 5 Runes UI Tree                                   │
└────────────────────────────────────────────────────────────────────────┘
```

### 1.2 Frontend Component Hierarchy

```
App.svelte (Three-Pane Shell + Theme Provider)
 ├─ Header (App title, Global filter triggers, Theme toggle)
 ├─ Pane 1: Sidebar (Width persisted in localStorage)
 │   ├─ SearchInput (Quick filter trigger)
 │   ├─ ModeSelector (Dir-Agent | Agent-Dir | Flat)
 │   ├─ GroupTree (Collapsible hierarchy)
 │   │   └─ GroupNodeItem (Directory / Agent node, session count badge)
 │   └─ AgentSummary (Bottom bar: Claude, Codex, OpenCode counts)
 ├─ Splitter (Draggable vertical separator)
 ├─ Pane 2: SessionList (Width persisted in localStorage)
 │   ├─ FilterSortBar (Status filters, sort mode selector)
 │   └─ VirtualList (Fixed-height row virtualizer)
 │       └─ SessionRow
 │           ├─ LiveDot (Pulsing green indicator if active PID)
 │           ├─ SessionTitle (Truncated prompt / title)
 │           ├─ AgentBadge (Claude, Codex, OpenCode color-coded)
 │           ├─ MessageCountBadge (User + Assistant sum)
 │           │   └─ CountTooltip (Breakdown: user · assistant · tools)
 │           └─ RelativeTime (Hover for full ISO timestamp)
 ├─ Splitter (Draggable vertical separator)
 └─ Pane 3: TranscriptView (Flex-1 main viewer)
     ├─ SessionHeader (Title, agent, model, cwd, branch, tokens, cost)
     │   └─ ActionToolbar (Copy resume cmd, Reveal file, Toggle meta)
     ├─ VirtualList (Variable-height message virtualizer)
     │   └─ MessageBubble (Role: user | assistant | system)
     │       ├─ MessageMeta (Role indicator, timestamp, model badge)
     │       └─ PartRenderer (Iterates over message.parts)
     │           ├─ TextPart (Markdown via marked + DOMPurify)
     │           ├─ ReasoningPart (Collapsible disclosure)
     │           ├─ ToolPart (Call + result pair, status, truncated blob loader)
     │           │   └─ SubagentLink (Drill-down to child session)
     │           ├─ PatchPart (File diff summary with +/- stats)
     │           ├─ CompactionPart (Context compaction boundary divider)
     │           └─ NoticePart (System warnings and latch events)
     └─ ScrollToBottomPill (Floating jump button when scrolled up)
```

### 1.3 Directory Structure (`frontend/src/`)

```
frontend/src/
├── App.svelte                     # Shell layout, pane splitters, theme host
├── main.ts                        # Bootstrap, Svelte 5 mount, link interceptor
├── style.css                      # CSS tokens, theme variables, resets
├── vite-env.d.ts                  # Ambient Vite & Wails runtime types
└── lib/
    ├── api.ts                     # Authoritative BackendAPI contract + runtime bridge
    ├── types.ts                   # Frontend domain types mirrored from Go model
    ├── markdown.ts                # marked config + DOMPurify sanitizer pipeline
    ├── highlight.ts               # highlight.js syntax highlighter integration
    ├── date.ts                    # Relative and absolute timestamp formatters
    ├── format.ts                  # Token and USD cost formatters
    ├── mock/
    │   ├── fixtures.ts            # Sanitized golden sessions for browser dev
    │   └── mockApi.ts             # In-memory implementation of BackendAPI
    ├── stores/
    │   ├── appState.svelte.ts     # Global reactive state (Svelte 5 runes)
    │   ├── theme.svelte.ts        # Dark/light theme observer & persistence
    │   └── preferences.svelte.ts  # Pane widths, filters, UI toggle flags
    └── components/
        ├── common/
        │   ├── Badge.svelte, Splitter.svelte, Tooltip.svelte
        │   └── EmptyState.svelte, ErrorState.svelte, LoadingSpinner.svelte
        ├── sidebar/
        │   ├── GroupTree.svelte, GroupNodeItem.svelte
        │   └── ModeSelector.svelte, AgentSummary.svelte
        ├── sessionlist/
        │   ├── SessionList.svelte, SessionRow.svelte, VirtualList.svelte
        └── transcript/
            ├── TranscriptView.svelte, SessionHeader.svelte, MessageBubble.svelte
            ├── CodeBlock.svelte
            └── parts/
                ├── TextPart.svelte, ReasoningPart.svelte, ToolPart.svelte
                ├── PatchPart.svelte, CompactionPart.svelte, SubagentLink.svelte
                └── NoticePart.svelte
```

---

## 2. Technical Stack & Environment Verification

### 2.1 Host Environment & Dependencies
- **OS:** Ubuntu 26.04 LTS (x86_64).
- **Go:** 1.26.8 (`go version go1.26.8 linux/amd64`).
- **Node & Package Managers:** Node.js v24.18.0, npm 11.16.0, pnpm 11.27.1.
- **Native GUI Libraries:** `gtk+-3.0` 3.24.52 (`libgtk-3-dev`), `webkit2gtk-4.1` 2.52.6 (`libwebkit2gtk-4.1-dev`).
- **Package Manager Choice:** **npm** is configured as standard in `wails.json` (`frontend:install`: `npm install`, `frontend:build`: `npm run build`) because npm is pre-installed with Node on all platforms without requiring mise or corepack. `pnpm` remains fully usable locally without lockfile conflicts.

### 2.2 Wails v2 Configuration & WebKitGTK Workarounds
- **Version:** Wails `v2.14.0` (stable release line; v3 is avoided per SPEC §2).
- **Linux Build Tag:** `-tags webkit2_41` (Ubuntu 26.04 provides `webkit2gtk-4.1`).
- **CLI Install:** `go install github.com/wailsapp/wails/v2/cmd/wails@v2.14.0`. Verify with `wails doctor`.
- **WebKitGTK Blank Window Workaround:** On Linux with NVIDIA or certain Mesa drivers, WebKitGTK 4.1 DMA-BUF texture allocation can fail, leaving the window blank or black.
  - Fix: In `cmd/agent-sessions/main.go`, set `WEBKIT_DISABLE_DMABUF_RENDERER=1` in `init()` before `wails.Run()`.
  - In Wails options, configure `Linux: &linux.Options{ WebviewGpuPolicy: linux.WebviewGpuPolicyOnDemand }`.

### 2.3 Svelte 5 & Vite Upgrade Path
The upstream Wails `svelte-ts` template uses older conventions and `svelte-preprocess`. We modernize the scaffold:
- Dependencies: `svelte: ^5.57.1`, `@sveltejs/vite-plugin-svelte: ^7.3.1`, `vite: ^8.3.1`, `typescript: ^5.6.3`, `svelte-check: ^4.7.6`.
- Remove `svelte-preprocess` in favor of `@sveltejs/vite-plugin-svelte`'s built-in `vitePreprocess()`.
- Use Svelte 5 runes (`$state`, `$derived`, `$effect`, `$props`, snippets).

### 2.4 Syntax Highlighting Choice: `highlight.js` vs `shiki`
In WebKitGTK 2.52 on Linux, `shiki` requires `vscode-oniguruma` WebAssembly initialization (150–250 ms freeze) and generates deep inline-style `<span>` trees that slow layout on long transcripts. `highlight.js`:
- Pure synchronous JavaScript (< 5 ms initialization, 0 WASM overhead).
- Uses class names (`<span class="hljs-keyword">`), allowing instant theme switching via CSS variables without re-highlighting.
- Renders 40 MB transcripts at 60 FPS.
**Decision:** Standardize on `highlight.js` for M1.

---

## 3. Detailed Task Specifications (M1-15 … M1-28)

---

## M1-15 Wails v2 scaffold & Linux runtime setup (3 h)

Set up the GUI application skeleton with Wails v2, configure Linux build flags (`webkit2_41`), integrate the WebKitGTK DMA-BUF fix, and scaffold the modernized Svelte 5 frontend.

**Files**
- `cmd/agent-sessions/main.go`
- `internal/app/app.go`
- `wails.json`
- `frontend/package.json`
- `frontend/vite.config.ts`, `frontend/svelte.config.js`, `frontend/tsconfig.json`, `frontend/index.html`
- `Makefile`

**Signatures & Configuration**

```go
// cmd/agent-sessions/main.go
package main

func init() {
    if os.Getenv("WEBKIT_DISABLE_DMABUF_RENDERER") == "" {
        _ = os.Setenv("WEBKIT_DISABLE_DMABUF_RENDERER", "1")
    }
}

func main() {
    application := app.NewApp()
    err := wails.Run(&options.App{
        Title:            "Agent Sessions",
        Width: 1280, Height: 820, MinWidth: 960, MinHeight: 600,
        AssetServer:      &assetserver.Options{Assets: assets},
        OnStartup:        application.OnStartup,
        Bind:             []interface{}{application},
        Linux:            &linux.Options{WebviewGpuPolicy: linux.WebviewGpuPolicyOnDemand, ProgramName: "agent-sessions"},
    })
    if err != nil { log.Fatal(err) }
}
```

```javascript
// frontend/svelte.config.js
import { vitePreprocess } from '@sveltejs/vite-plugin-svelte';
export default { preprocess: vitePreprocess() };
```

**Steps**
1. Check `wails` CLI; if absent, install `go install github.com/wailsapp/wails/v2/cmd/wails@v2.14.0`. Run `wails doctor`.
2. Scaffold project structure, pointing Go entrypoint to `cmd/agent-sessions/main.go`.
3. In `frontend/package.json`, set `svelte: ^5.57.1`, `@sveltejs/vite-plugin-svelte: ^7.3.1`, `vite: ^8.3.1`, `typescript: ^5.6.3`, and `svelte-check: ^4.7.6`. Remove `svelte-preprocess`.
4. Update `frontend/src/main.ts` to Svelte 5: `mount(App, { target: document.getElementById('app')! })`.
5. Update root `Makefile` with `make dev` (`wails dev -tags webkit2_41`) and `make app` (`wails build -tags webkit2_41 -clean`).

**Edge Cases & Tests**
- Missing system dev headers: Check for `pkg-config --exists webkit2gtk-4.1 gtk+-3.0`; fail fast with installation hint.
- Headless CI execution: Guard GUI builds behind tags so `make test` never requires a display server.
- Tests: `go build -tags webkit2_41 ./cmd/agent-sessions`, `cd frontend && npm run check`, launch test with `wails dev`.

**Done when:** `make app` builds `build/bin/agent-sessions` that launches a functional window on Ubuntu 26.04 with WebKitGTK 4.1.

---

## M1-16 App service & bridge API layer (3 h)

Implement the bound `App` service in `internal/app` exposing grouping, session lists, metadata, and paged transcripts to the frontend. Implement `frontend/src/lib/api.ts` wrapping generated bindings with a complete mock fallback.

**Files**
- `internal/app/service.go`, `internal/app/types.go`, `internal/app/lru.go`
- `frontend/src/lib/api.ts`, `frontend/src/lib/types.ts`
- `frontend/src/lib/mock/fixtures.ts`, `frontend/src/lib/mock/mockApi.ts`

**Signatures & Types**

```go
// internal/app/types.go
package app

type GroupMode string
const (
    GroupModeDirAgent GroupMode = "dir-agent"
    GroupModeAgentDir GroupMode = "agent-dir"
    GroupModeFlat     GroupMode = "flat"
)

type FilterOpts struct {
    Agent string `json:"agent,omitempty"`; Query string `json:"query,omitempty"`
    LiveOnly bool `json:"liveOnly,omitempty"`; Archived bool `json:"archived,omitempty"`
    HasSubagents bool `json:"hasSubagents,omitempty"`
}

type SortOpts struct { Field string `json:"field"`; Desc bool `json:"desc"` }

type GroupNode struct {
    Key string `json:"key"`; Label string `json:"label"`; Secondary string `json:"secondary,omitempty"`
    Agent string `json:"agent,omitempty"`; CWD string `json:"cwd,omitempty"`; CWDMissing bool `json:"cwdMissing,omitempty"`
    SessionCount int `json:"sessionCount"`; Children []GroupNode `json:"children,omitempty"`
}

type MessagesPage struct {
    Messages []model.Message `json:"messages"`; Offset int `json:"offset"`; Limit int `json:"limit"`
    TotalCount int `json:"totalCount"`; HasMore bool `json:"hasMore"`
}

type BlobResponse struct { Data string `json:"data"`; Mime string `json:"mime"`; IsBinary bool `json:"isBinary"` }
```

```go
// internal/app/service.go
package app

func (a *App) ListGroups(mode GroupMode, filter FilterOpts) ([]GroupNode, error)
func (a *App) ListSessions(groupKey string, filter FilterOpts, sort SortOpts) ([]model.SessionMeta, error)
func (a *App) GetSessionMeta(ref model.SessionRef) (model.SessionMeta, error)
func (a *App) GetMessages(ref model.SessionRef, offset int, limit int) (MessagesPage, error)
func (a *App) GetBlob(ref model.SessionRef, key string) (BlobResponse, error)
func (a *App) CopyResumeCommand(ref model.SessionRef) (string, error)
func (a *App) RevealSource(ref model.SessionRef) error
func (a *App) Diagnostics() (provider.Diagnostics, error)
```

```typescript
// frontend/src/lib/api.ts
export interface BackendAPI {
  listGroups(mode: GroupMode, filter?: FilterOpts): Promise<GroupNode[]>;
  listSessions(groupKey: string, filter?: FilterOpts, sort?: SortOpts): Promise<SessionMeta[]>;
  getSessionMeta(ref: SessionRef): Promise<SessionMeta>;
  getMessages(ref: SessionRef, offset: number, limit: number): Promise<MessagesPage>;
  getBlob(ref: SessionRef, key: string): Promise<BlobResponse>;
  copyResumeCommand(ref: SessionRef): Promise<string>;
  revealSource(ref: SessionRef): Promise<void>;
  getDiagnostics(): Promise<Diagnostics>;
  openURL(url: string): Promise<void>;
  onEvent(name: string, callback: (...data: any[]) => void): () => void;
}
export const api: BackendAPI = createAPI();
```

**Steps**
1. Implement `transcriptLRU` in `internal/app/lru.go` (mutex-protected map + list, capacity 4).
2. Implement `App` methods wrapping `scan.Catalog` and `group.Engine`.
3. In `GetMessages`, check LRU; on miss, call `provider.Load(ctx, ref)`, insert into LRU, slice `[offset : min(offset+limit, len)]`, return `MessagesPage`.
4. In `GetBlob`, call `provider.Blob(ctx, ref, key)`. Return text as UTF-8 or binary as base64.
5. In `frontend/src/lib/api.ts`, inspect `window.go?.app?.App`. If present, bind to Wails JS bindings; otherwise return `MockBackendAPI`.

**Wails v2 Binding Quirks to Handle**
- Parameters and returns must be JSON-serializable.
- `time.Time` fields serialize to RFC3339 strings in TypeScript.
- `json.RawMessage` maps to `number[]` unless tagged with `ts_type:"any"`. In `frontend/src/lib/types.ts`, type `ToolCall.input` as `any`.
- Rejected Go errors reject JavaScript Promises; `api.ts` normalizes them into structured error objects.

**Tests**
- `internal/app/service_test.go`: Test LRU caching and pagination bounds for `GetMessages`.
- `frontend/src/lib/api.test.ts`: Test that `MockBackendAPI` conforms to `BackendAPI`.

**Done when:** Frontend calls all methods of `api` in both mock mode (browser) and live mode (Wails) without errors.

---

## M1-17 Frontend shell, splitters & theme system (3 h)

Construct the three-pane application layout with draggable splitters, persistent width settings, and a CSS custom property theme engine supporting system preferences.

**Files**
- `frontend/src/App.svelte`, `frontend/src/style.css`
- `frontend/src/lib/components/common/Splitter.svelte`
- `frontend/src/lib/stores/theme.svelte.ts`, `frontend/src/lib/stores/preferences.svelte.ts`, `frontend/src/lib/stores/appState.svelte.ts`

**Signatures & CSS Structure**

```typescript
// frontend/src/lib/stores/theme.svelte.ts
export type ThemeMode = 'system' | 'light' | 'dark';
export class ThemeStore {
  mode = $state<ThemeMode>('system');
  resolved = $state<'light' | 'dark'>('dark');
  init(): void; setMode(mode: ThemeMode): void; toggle(): void;
}
export const theme = new ThemeStore();
```

```css
/* frontend/src/style.css tokens */
:root {
  --bg-primary: #ffffff; --bg-secondary: #f8fafc; --bg-tertiary: #f1f5f9;
  --border-color: #e2e8f0; --text-primary: #0f172a; --text-secondary: #475569;
  --text-muted: #94a3b8; --accent-color: #2563eb;
  --agent-claude: #d97757; --agent-codex: #10a37f; --agent-opencode: #3b82f6;
}
[data-theme="dark"] {
  --bg-primary: #0f172a; --bg-secondary: #1e293b; --bg-tertiary: #334155;
  --border-color: #334155; --text-primary: #f8fafc; --text-secondary: #cbd5e1;
  --text-muted: #64748b; --accent-color: #3b82f6;
}
```

**Steps**
1. Implement `Splitter.svelte`: Capture `pointerdown`, listen to window `pointermove` and `pointerup`. Constrain widths: Sidebar min 200px / max 450px; SessionList min 260px / max 550px; TranscriptView flex-1 (min 400px). Persist widths to `localStorage` key `agent-sessions:pane-widths`.
2. Implement `theme.svelte.ts`: Match `window.matchMedia('(prefers-color-scheme: dark)')`. Update `document.documentElement.setAttribute('data-theme', resolved)`. Persist mode preference (`system` | `light` | `dark`).
3. Set up `App.svelte` layout: Pane 1 (`<aside>`), Splitter 1, Pane 2 (`<section>`), Splitter 2, Pane 3 (`<main>`).

**Edge Cases & Tests**
- Window resized below 960px: Proportionally clamp widths so Transcript pane remains visible.
- Tests: Vitest test verifying theme preference persistence and DOM attribute toggle; component test verifying `Splitter` clamps within min/max bounds.

**Done when:** The three panes resize smoothly, persist dimensions across restarts, and switch themes according to system settings and user toggle.

---

## M1-18 Sidebar group tree & mode switching (3 h)

Build the sidebar navigation tree supporting the three required grouping modes (`dir-agent`, `agent-dir`, `flat`), session counts, collapse/expand states, and missing directory indicators.

**Files**
- `frontend/src/lib/components/sidebar/GroupTree.svelte`, `GroupNodeItem.svelte`, `ModeSelector.svelte`, `AgentSummary.svelte`
- `frontend/src/lib/stores/appState.svelte.ts`

**Signatures & Props**

```typescript
// GroupNodeItem.svelte
interface Props {
  node: GroupNode; level?: number; selectedKey: string | null; onSelect: (node: GroupNode) => void;
}

// appState.svelte.ts (Excerpt)
export class AppState {
  groupMode = $state<GroupMode>('dir-agent');
  selectedGroupKey = $state<string | null>(null);
  selectedSessionRef = $state<SessionRef | null>(null);
  groups = $state<GroupNode[]>([]);
  collapsedKeys = $state<Set<string>>(new Set());
  toggleCollapsed(key: string): void; selectGroup(key: string): void; loadGroups(): Promise<void>;
}
```

**Steps**
1. Implement `ModeSelector.svelte`: Three-button segmented toggle for `dir-agent`, `agent-dir`, `flat`. On click, update `appState.groupMode` and invoke `api.listGroups(mode)`.
2. Implement recursive `GroupNodeItem.svelte`: Indent by `level * 12px`. Show disclosure triangle (▶ / ▼) for parent nodes; track collapse in `appState.collapsedKeys`. Render folder icon for directory nodes; agent badge for agent nodes. If `node.cwdMissing` is true, render folder in dimmed style with a `[missing]` badge. Display session count badge on the right.
3. Implement `AgentSummary.svelte` at the bottom of the sidebar: Display total sessions per agent (`Claude: 34`, `Codex: 0`, `OpenCode: 64`).

**Edge Cases & Tests**
- Deeply nested paths: Truncate intermediate directory segments with ellipsis. Directory with 0 direct sessions: Auto-expand and select first child node.
- Tests: Verify group tree renders all 3 modes using mock fixtures; verify missing directory flag renders warning badge.

**Done when:** Sidebar correctly navigates directory and agent hierarchies, collapses/expands nodes, switches grouping modes instantaneously, and filters the session list.

---

## M1-19 Virtualized session list & count breakdown (4 h)

Implement the middle column virtualized session list. Display status indicators, agent badges, relative time, and the authoritative message count with tooltip breakdown.

**Files**
- `frontend/src/lib/components/sessionlist/SessionList.svelte`, `SessionRow.svelte`, `CountTooltip.svelte`, `VirtualList.svelte`
- `frontend/src/lib/date.ts`

**Signatures & Props**

```typescript
// SessionRow.svelte
interface Props {
  session: SessionMeta; isSelected: boolean; onSelect: (session: SessionMeta) => void;
}

// frontend/src/lib/date.ts
export function formatRelativeTime(dateStr: string): string; // "2m", "3h", "5d"
export function formatAbsoluteTime(dateStr: string): string; // "2026-09-28 14:32:05 UTC"
```

**Steps**
1. Implement fixed-height virtualizer in `VirtualList.svelte` (item height: 58px): Calculate start/end indexes from scroll container `scrollTop` and client height; maintain overscan buffer of 5 items.
2. Implement `SessionRow.svelte`:
   - **Live indicator:** If `session.live` is true, render a pulsing green dot with title `"Active session"`.
   - **Title:** `session.title || session.firstPrompt || "(untitled)"` (single-line truncation).
   - **Agent Badge:** Color-coded badge (`claude-code`, `codex`, `opencode`).
   - **Message Count:** Render `💬 {session.counts.user + session.counts.assistant}`.
   - **Relative Time:** Formatted relative time (`2m`, `1d`).
3. Implement `CountTooltip.svelte`: On hover over message count badge, display tooltip: `user {session.counts.user} · assistant {session.counts.assistant} · tool calls {session.counts.toolCalls}`. On hover over relative time, display full ISO timestamp `session.updatedAt`.
4. Keyboard navigation: Arrow Up/Down moves selection; Enter selects.

**Edge Cases & Tests**
- Rapid live updates: Use `font-variant-numeric: tabular-nums` to prevent layout shifts. 1,000+ sessions in a group: Virtual list keeps rendered DOM nodes capped under 30.
- Tests: Benchmark test with 2,000 items verifying DOM nodes stay capped at < 30; unit test verifying message count computation matches `counts.user + counts.assistant` and excludes tool calls.

**Done when:** Session list renders 1,000+ sessions smoothly, correctly displays live dots, badges, message counts, and detailed count tooltips on hover.

---

## M1-20 Virtualized transcript viewer base & markdown sanitization (4 h)

Build the foundational transcript viewer with a virtualized message list, distinct user/assistant chat bubbles, and secure markdown rendering via `marked` + `DOMPurify`.

**Files**
- `frontend/src/lib/components/transcript/TranscriptView.svelte`, `MessageBubble.svelte`
- `frontend/src/lib/markdown.ts`, `frontend/src/lib/components/common/LoadingSpinner.svelte`

**Signatures & Sanitizer Configuration**

```typescript
// frontend/src/lib/markdown.ts
import { marked } from 'marked';
import DOMPurify from 'dompurify';

export function renderMarkdown(source: string): string {
  const rawHtml = marked.parse(source, { gfm: true, breaks: true }) as string;
  return DOMPurify.sanitize(rawHtml, {
    ALLOWED_TAGS: [
      'p', 'br', 'strong', 'em', 'code', 'pre', 'blockquote',
      'ul', 'ol', 'li', 'table', 'thead', 'tbody', 'tr', 'th', 'td',
      'h1', 'h2', 'h3', 'h4', 'h5', 'h6', 'hr', 'a', 'span'
    ],
    ALLOWED_ATTR: ['href', 'target', 'rel', 'class'],
    FORBID_TAGS: ['style', 'script', 'iframe', 'object', 'embed', 'form'],
    FORBID_ATTR: ['onerror', 'onload', 'onclick', 'onmouseover']
  });
}

// MessageBubble.svelte
interface Props {
  message: Message; showMeta: boolean; onChildSessionClick?: (ref: SessionRef) => void;
}
```

**Steps**
1. Configure `marked` with GitHub Flavored Markdown (tables, task lists, breaks).
2. Configure `DOMPurify` to sanitize all rendered HTML and prevent script execution or frame injection.
3. Implement `TranscriptView.svelte` with dynamic height virtualization (`@tanstack/svelte-virtual` with `measureElement`): Handle variable message heights (40px user prompt to 1,200px multi-tool assistant turn). Support auto-scroll to top when switching sessions.
4. Implement `MessageBubble.svelte`:
   - Role User: Right-aligned or border-accented bubble, prompt styling.
   - Role Assistant: Full-width pane, assistant avatar, model badge.
   - Role System: Centered subtle notice bubble; hidden if `message.isMeta && !appState.showMeta`.

**Edge Cases & Tests**
- Malicious HTML in transcripts (`<img src=x onerror=...>`, `<script>` tags): Neutralized by DOMPurify.
- Massive single messages (> 50,000 words): Style with `contain: content` to avoid layout freeze.
- Tests: Security test feeding XSS vectors verifying DOMPurify neutralizes them; layout test verifying alternating user/assistant bubbles.

**Done when:** Transcripts render with clean user and assistant bubbles, markdown formatting works, and untrusted HTML is securely sanitized.

---

## M1-21 Code syntax highlighting & copy interaction (2 h)

Integrate `highlight.js` for syntax highlighting within markdown code blocks and add interactive "Copy" buttons with visual feedback.

**Files**
- `frontend/src/lib/highlight.ts`, `frontend/src/lib/components/transcript/CodeBlock.svelte`, `frontend/src/lib/markdown.ts`

**Signatures & Props**

```typescript
// frontend/src/lib/highlight.ts
export function highlightCode(code: string, language?: string): string;
export function detectLanguage(code: string): string;

// CodeBlock.svelte
interface Props { code: string; language?: string; }
```

**Steps**
1. Register common languages in `frontend/src/lib/highlight.ts`: Go, TypeScript, JavaScript, Python, Bash/Shell, JSON, YAML, Markdown, HTML, CSS, Rust, SQL, Diff. Default to plaintext for unknown languages.
2. In markdown renderer, route code blocks to `highlightCode(code, lang)`. Wrap output in `<div class="code-block-container">` with a header bar.
3. Implement `CodeBlock.svelte`: Top bar displaying detected language name and a "Copy" button. On click, copy code to clipboard using `api.copyText()` or `navigator.clipboard.writeText(code)`. Show "Copied!" checkmark feedback for 1,500 ms.
4. Map `highlight.js` theme tokens to CSS custom properties (`.hljs-keyword`, `.hljs-string`, `.hljs-comment`, etc.) so code blocks adapt to light/dark themes without re-highlighting.

**Edge Cases & Tests**
- Non-standard language tag (````golang` vs ````go`): Alias mapping handles common variations. Large snippets (> 10,000 lines): Apply CSS `max-height: 600px` with vertical scrollbar.
- Tests: Unit test verifying Go, TypeScript, and JSON snippets receive valid highlight classes; verify clipboard copy callback is invoked on button click.

**Done when:** Code blocks display accurate syntax highlighting matching the active theme, and copy buttons provide immediate visual feedback.

---

## M1-22 Transcript part renderers & subagent drill-down (4 h)

Implement dedicated renderers for every `PartKind` defined in M0: paired tool calls with results, collapsible reasoning blocks, patch/diff summaries, compaction dividers, and clickable subagent links.

**Files**
- `frontend/src/lib/components/transcript/parts/ToolPart.svelte`, `ReasoningPart.svelte`, `PatchPart.svelte`
- `frontend/src/lib/components/transcript/parts/CompactionPart.svelte`, `SubagentLink.svelte`, `NoticePart.svelte`

**Component Props**

```typescript
// ToolPart.svelte
interface Props { tool: ToolCall; sessionRef: SessionRef; onOpenChild?: (ref: SessionRef) => void; }

// ReasoningPart.svelte
interface Props { text: string; defaultExpanded?: boolean; }

// PatchPart.svelte
interface Props { files: string[]; text?: string; }

// CompactionPart.svelte
interface Props { text: string; }
```

**Steps**
1. **ToolPart:** Pair tool call and result in a single card. Status header: Tool name (`bash`, `edit`, `read`), status badge (`✓ completed` green, `✗ error` red, `⏳ pending` amber). Collapsible input parameters formatted as highlighted JSON. Result output: Monospaced output box. If `tool.outputTruncated` is true, display a "Show full output" button fetching the full text via `api.getBlob(ref, tool.outputRef)`. If `tool.child` is present, render `SubagentLink.svelte`.
2. **SubagentLink:** Render button: `Subagent: {tool.child.id} (→ Open Session)`. On click, invoke `appState.selectSession(tool.child)` to switch viewer to the child session.
3. **ReasoningPart:** Render collapsed `<details class="reasoning-block">` with summary `"Thinking process..."`.
4. **PatchPart:** Display modified file list with additions/deletions badges (`+40 -12`).
5. **CompactionPart:** Centered divider pill: `"── Context compacted ──"`. Display first line; expandable to show full summary.
6. **Meta message toggle:** Filter out parts/messages where `isMeta` is true unless `appState.showMeta` is active.

**Edge Cases & Tests**
- Missing tool output: Display animated loader status. Nested subagents: Maintain history stack in appState for "Back to Parent" breadcrumb.
- Tests: Fixture test rendering `split_assistant.jsonl`, `compaction.jsonl`, and `meta.jsonl`; subagent navigation test verifying click triggers selection of child `SessionRef`.

**Done when:** All part types (`text`, `reasoning`, `tool`, `patch`, `file`, `compaction`, `notice`) render with specialized components and subagent links navigate to child sessions.

---

## M1-23 Session metadata header & metrics (2 h)

Build the top transcript header pane displaying session title, agent badge, model name, working directory, git branch, message count, token meters, cost, and quick action buttons.

**Files**
- `frontend/src/lib/components/transcript/SessionHeader.svelte`, `frontend/src/lib/components/common/Badge.svelte`
- `frontend/src/lib/format.ts`

**Signatures & Props**

```typescript
// frontend/src/lib/format.ts
export function formatTokens(tokens: number): string; // 128400 -> "128.4k", 1500000 -> "1.5M"
export function formatCost(usd?: number): string;     // 0.423 -> "$0.42"

// SessionHeader.svelte
interface Props {
  meta: SessionMeta; showMeta: boolean; onToggleMeta: () => void; onResume: () => void; onReveal: () => void;
}
```

**Steps**
1. Implement `formatTokens` and `formatCost` in `frontend/src/lib/format.ts`.
2. Construct `SessionHeader.svelte`:
   - Line 1: Title in large typography (`font-weight: 600`), agent badge, model pill (`claude-3-7-sonnet`, `gpt-4o`).
   - Line 2: Folder icon + normalized `cwd` (truncated with tooltip for full path); Git branch icon + `gitBranch` (if present); message count `💬 {meta.counts.user + meta.counts.assistant}`.
   - Line 3: Token metrics summary: `In: {formatTokens(tokens.input)} · Out: {formatTokens(tokens.output)} · Cache: {formatTokens(tokens.cacheRead)} · Cost: {formatCost(meta.costUsd)}`.
   - Line 4: Action button bar: `[Copy Resume Cmd]`, `[Reveal in Files]`, `[Toggle Meta/System]`.
3. If session is a subagent (`meta.parentId` non-empty), show top banner: `"Subagent session of parent: {meta.parentId}"` with back link.

**Edge Cases & Tests**
- Missing Git branch or cost: Omit absent fields gracefully without awkward spacing.
- Tests: Component test verifying header displays all metadata fields from sample `SessionMeta`; verify clicking "Toggle Meta" invokes `onToggleMeta` callback.

**Done when:** Session header displays all metadata, tokens, and action buttons cleanly, adapting responsively to narrow pane widths.

---

## M1-24 Progressive loading for large transcripts & LRU cache (4 h)

Implement paged transcript retrieval and load-on-scroll virtualization so that transcripts up to 40 MB display their initial screen in under 1 second without blocking the UI thread.

**Files**
- `internal/app/service.go`, `internal/app/lru.go`
- `frontend/src/lib/components/transcript/TranscriptView.svelte`, `frontend/src/lib/stores/appState.svelte.ts`

**Signatures & LRU Design**

```go
// internal/app/lru.go
package app

type transcriptLRU struct {
    mu sync.Mutex; capacity int; items map[string]*list.Element; evict *list.List
}
type lruEntry struct { key string; transcript *model.Transcript }

func newTranscriptLRU(capacity int) *transcriptLRU
func (c *transcriptLRU) Get(key string) (*model.Transcript, bool)
func (c *transcriptLRU) Put(key string, t *model.Transcript)
```

```typescript
// appState.svelte.ts (Paged Loading Excerpt)
export class AppState {
  currentMessages = $state<Message[]>([]); totalMessages = $state<number>(0);
  isLoadingMore = $state<boolean>(false); hasMoreMessages = $state<boolean>(false);
  async loadInitialTranscript(ref: SessionRef): Promise<void>;
  async loadMoreMessages(): Promise<void>;
}
```

**Steps**
1. Implement 4-entry LRU in Go `internal/app/lru.go`. When `GetMessages` is called: Check LRU cache by `ref.Key()`. On miss: Call `provider.Load(ctx, ref)` (38 MB JSONL parse takes ~300–600 ms). Store in LRU. Slice messages for requested `offset` and `limit` (default page size: 100 messages). Return `MessagesPage`.
2. In `TranscriptView.svelte`: On session selection: Fetch first page (`offset = 0, limit = 100`). Render immediately (< 1 s). Virtual list scroll listener: When scroll approaches bottom, trigger `loadMoreMessages()` with next offset. Append fetched messages to `appState.currentMessages`.
3. If parsing a huge file ever exceeds 1s on slower disks, provider can support stream-parsing (`LoadStream`) to yield the first 100 messages before the full file pass completes.

**Edge Cases & Tests**
- Rapid session switching: Discard in-flight pagination requests for previous sessions using an incrementing request ID. Appended messages in live sessions: Update `totalMessages` and append new records without resetting scroll position.
- Tests: Benchmark test measuring time to load first page of 38 MB session (assert duration < 1,000 ms); LRU test verifying 5th session evicts 1st while keeping 4 most recent.

**Done when:** The 38 MB session displays its first screen in under 1 second, and subsequent pages load smoothly on scroll.

---

## M1-25 Desktop actions: resume command & reveal source (2 h)

Implement desktop actions to copy the provider-specific resume command (including working directory change) and reveal the source session file in the system file manager.

**Files**
- `internal/app/service.go`, `internal/app/actions.go`
- `frontend/src/lib/components/transcript/SessionHeader.svelte`, `frontend/src/lib/components/common/Toast.svelte`

**Signatures & Implementation**

```go
// internal/app/actions.go
package app

func formatResumeCommand(cmd provider.Command, cwd string) string {
    var parts []string
    for _, arg := range cmd.Argv { parts = append(parts, shellEscape(arg)) }
    cmdStr := strings.Join(parts, " ")
    if cwd != "" { return fmt.Sprintf("cd %s && %s", shellEscape(cwd), cmdStr) }
    return cmdStr
}
```

```go
// internal/app/service.go
func (a *App) CopyResumeCommand(ref model.SessionRef) (string, error) {
    prov, ok := a.providers.Get(ref.Agent)
    if !ok { return "", fmt.Errorf("unknown provider %s", ref.Agent) }
    meta, err := a.catalog.Get(ref)
    if err != nil { return "", err }
    cmd := prov.ResumeCommand(meta)
    formatted := formatResumeCommand(cmd, meta.CWD)
    if err := runtime.ClipboardSetText(a.ctx, formatted); err != nil {
        return "", fmt.Errorf("clipboard set error: %w", err)
    }
    return formatted, nil
}

func (a *App) RevealSource(ref model.SessionRef) error {
    meta, err := a.catalog.Get(ref)
    if err != nil { return err }
    dir := filepath.Dir(meta.SourcePath)
    return exec.Command("xdg-open", dir).Start()
}
```

**Steps**
1. Implement `formatResumeCommand` with POSIX shell argument escaping.
2. In `CopyResumeCommand`: Resolve provider `ResumeCommand`, format with `cd <cwd> && <cmd>`, call `runtime.ClipboardSetText(ctx, formatted)`.
3. In `RevealSource`: Resolve `meta.SourcePath`. On Linux, execute `xdg-open` on `filepath.Dir(sourcePath)`. On Darwin (M5 stub), execute `open -R <sourcePath>`.
4. In `SessionHeader.svelte`: Wire "Copy Resume" button (calls `api.copyResumeCommand(ref)`, shows toast) and "Reveal in Files" button (calls `api.revealSource(ref)`).

**Edge Cases & Tests**
- Missing or deleted source file: Return descriptive error and display user-facing alert toast. Spaces in CWD path: Handled by shell quoting.
- Tests: Unit test for `formatResumeCommand` verifying `cd "/path with spaces" && claude --resume "uuid"`; mock test verifying `CopyResumeCommand` invokes clipboard copy.

**Done when:** Clicking "Copy Resume" copies a functional resume command to the clipboard, and "Reveal in Files" opens the file manager at the session file location.

---

## M1-26 Empty, loading, and error states (2 h)

Design and implement polished empty, error, and loading states across the three panes.

**Files**
- `frontend/src/lib/components/common/EmptyState.svelte`, `ErrorState.svelte`, `LoadingSpinner.svelte`
- `frontend/src/lib/components/sessionlist/SessionListSkeleton.svelte`

**Scenarios & UI Behaviors**
1. **No Agents Detected:** Displayed when 0 sessions are found. Guidance lists scanned roots (`~/.claude`, `~/.codex`, `~/.local/share/opencode`).
2. **Empty Group Selection:** Displayed when selected directory/agent has 0 matching sessions. Includes `"Clear filters"` button.
3. **No Session Selected:** Displayed in Pane 3 on startup. Includes shortcut hint (`j`/`k` to navigate, `Enter` to open).
4. **Session File Missing / Deleted:** Displayed in Pane 3 if a selected session file was deleted from disk. Explains source path was moved or trashed.
5. **Scan / Provider Error:** Displayed if a provider encounters a filesystem permission error, with collapsible technical diagnostics.
6. **Loading Skeletons:** Animated placeholder pulse rows in the session list while scanning.

**Steps**
1. Implement reusable `EmptyState.svelte` with `icon`, `title`, `description`, and action slot.
2. Implement `ErrorState.svelte` with retry button.
3. Implement `SessionListSkeleton.svelte` showing 6 pulsing placeholder rows.
4. Integrate states into `GroupTree.svelte`, `SessionList.svelte`, and `TranscriptView.svelte`.

**Edge Cases & Tests**
- Provider scanner timeout: Surface error with retry button instead of endless loading spinner.
- Tests: Vitest component tests verifying each empty state renders its specific action and description.

**Done when:** All empty, loading, and error scenarios have dedicated, informative UI views that prevent blank panes.

---

## M1-27 WebView hardening & navigation security (2 h)

Harden the Wails WebView environment: configure strict Content Security Policy, intercept all external link clicks to open in the system default browser, and prevent remote network requests.

**Files**
- `frontend/index.html`, `frontend/src/lib/api.ts`
- `internal/app/security.go`, `internal/app/service.go`

**Signatures & CSP Definition**

```html
<!-- frontend/index.html -->
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8"/>
    <meta content="width=device-width, initial-scale=1.0" name="viewport"/>
    <meta http-equiv="Content-Security-Policy" content="
        default-src 'none';
        script-src 'self';
        style-src 'self' 'unsafe-inline';
        img-src 'self' data: blob:;
        font-src 'self';
        connect-src 'self' ipc: wails:;
        frame-src 'none'; object-src 'none'; base-uri 'none';
    "/>
    <title>Agent Sessions</title>
</head>
<body>
    <div id="app"></div>
    <script src="./src/main.ts" type="module"></script>
</body>
</html>
```

```go
// internal/app/security.go
package app

func (a *App) OpenURL(rawURL string) error {
    parsed, err := url.Parse(rawURL)
    if err != nil { return fmt.Errorf("invalid url: %w", err) }
    scheme := strings.ToLower(parsed.Scheme)
    if scheme != "http" && scheme != "https" { return fmt.Errorf("unsupported url scheme: %s", scheme) }
    runtime.BrowserOpenURL(a.ctx, parsed.String())
    return nil
}
```

```typescript
// frontend/src/lib/navigation.ts
export function setupLinkInterceptor(api: BackendAPI): void {
  document.addEventListener('click', (e) => {
    const target = (e.target as HTMLElement).closest('a');
    if (!target) return;
    const href = target.getAttribute('href');
    if (href && (href.startsWith('http://') || href.startsWith('https://'))) {
      e.preventDefault();
      api.openURL(href);
    }
  });
}
```

**Steps**
1. Insert strict CSP meta tag in `frontend/index.html`. Block remote scripts, fonts, and stylesheets.
2. Implement `OpenURL` in Go: Validate URL format, reject non-http/https schemes (`file:`, `javascript:`, `data:`), delegate to `runtime.BrowserOpenURL(a.ctx, url)`.
3. In `frontend/src/main.ts`, register global link interceptor (`setupLinkInterceptor`). Ensure transcript links do not navigate the WebView away from the app.

**Edge Cases & Tests**
- Malicious transcript markdown containing `<a href="javascript:alert(1)">`: Blocked by DOMPurify and rejected by `OpenURL`.
- Tests: Unit test for `OpenURL` verifying `javascript:` and `file:` schemes return an error; DOM test verifying external link click calls `api.openURL` and prevents default navigation.

**Done when:** Strict CSP is active, external links open in the system default browser, and the WebView cannot navigate away or load unauthorized remote resources.

---

## M1-28 MVP QA pass & performance benchmarks (4 h)

Execute a comprehensive quality assurance pass across all MVP components on real session data, measure against the SPEC §8 performance targets, and document benchmark numbers.

**Files**
- `docs/perf.md`, `scripts/qa-perf-bench.sh`

**QA Checklist & SPEC §8 Performance Targets**

| Metric | Target | Verification Method |
|---|---|---|
| Cold first scan | < 5.0 s (≈300 MB history) | Time first run after wiping cache |
| Warm start to first paint | < 300 ms | Time from binary launch to window DOM paint |
| Opening 38 MB session | < 1.0 s to first screen | Click 38 MB session; measure time to render page 1 |
| Session list scroll FPS | 60 FPS (1,000+ items) | Chrome DevTools FPS meter during rapid scroll |
| Transcript scroll FPS | 60 FPS (large session) | Scrolling virtualized message list |
| Memory consumption | < 200 MB RSS | Monitor process RSS under full transcript view |

**Systematic QA Procedure**
1. **Real Data Ingestion:** Scan local `~/.claude` (30 main sessions + subagents) and local `~/.local/share/opencode` (64 sessions). Verify all sessions appear with correct titles, timestamps, and message counts matching the CLI.
2. **Transcript Accuracy Verification:** Verify split assistant messages are merged into coherent bubbles; verify tool calls are paired with their results; verify compaction markers display token transitions and expand summary text; verify subagent link opens child session and back navigation returns to parent.
3. **Performance Profiling:** Measure 38 MB session load time using console performance markers (`performance.mark` / `measure`). Profile memory usage with `ps -o rss,vsz,cmd -p $(pgrep agent-sessions)`.
4. **Issue Remediation:** Fix any layout shifts, virtualizer jitter, or memory leaks discovered during testing. Record final benchmark figures in `docs/perf.md`.

**Done when:** All items on the QA checklist pass, performance meets or beats the SPEC §8 targets, and `docs/perf.md` is committed with verified benchmark results.

---

## 4. Proposed Contract Changes

The authoritative contracts defined in [docs/plan/M0.md](M0.md) are preserved without breaking changes. The following minor, backward-compatible refinements are proposed to optimize Wails binding generation and frontend integration:

1. **`model.ToolCall.Input` TypeScript Mapping:**
   - *Current definition:* `Input json.RawMessage `json:"input,omitempty"``
   - *Observation:* In Wails v2, `json.RawMessage` is an alias for `[]byte` (`[]uint8`), causing the generated TypeScript binding to emit `input?: number[]`.
   - *Proposed refinement:* Add `ts_type:"any"` struct tag:
     ```go
     Input json.RawMessage `json:"input,omitempty" ts_type:"any"`
     ```
   - *Effect:* Wails generates `input?: any` in `frontend/wailsjs/go/models.ts`, eliminating the need for client-side byte-array casting.

2. **`model.ToolCall.OutputRef` & `provider.Blob` Return Type:**
   - *Current definition:* `Blob(ctx, ref, key) ([]byte, error)`
   - *Observation:* Wails serializes raw `[]byte` as base64 strings across the IPC bridge.
   - *Proposed refinement:* In `internal/app/service.go`, wrap `Blob` results into `BlobResponse{Data: string, Mime: string, IsBinary: bool}` so UTF-8 text outputs (such as large terminal logs) are transferred as plain text strings without base64 decoding overhead.

3. **`model.MessageCounts.Total` in JSON:**
   - *Current definition:* `Total()` is a method on `MessageCounts`.
   - *Observation:* Wails JSON serialization ignores struct methods.
   - *Proposed refinement:* Retain the Go method and compute `counts.user + counts.assistant` in `frontend/src/lib/types.ts` helper, avoiding changes to Go struct storage.
