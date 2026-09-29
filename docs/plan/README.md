# Implementation plans

Detailed per-task plans for [TASKS.md](../../TASKS.md). Read them in order:
later milestones build on the contracts defined in M0.

| File | Covers |
|---|---|
| [M0.md](M0.md) | Conventions, **shared contracts** (model, provider interface, JSONL reader, path utils), Claude Code provider, CLI |
| [M1-providers.md](M1-providers.md) | OpenCode and Codex providers, scan orchestrator/catalog, grouping engine |
| [M1-gui.md](M1-gui.md) | Wails v2 + Svelte 5 app: bindings, `api.ts`, layout, session list, transcript viewer, security, QA |
| [M2.md](M2.md) | Cache index, incremental scans, FTS search, file watching, live updates, diagnostics |
| [M3-M5.md](M3-M5.md) | Filters, config, export, keyboard, stats; destructive actions; macOS port, packaging, CI |
| [M6.md](M6.md) | Portable bundles (export, import, opt-in restore) and cross-agent handoff; bundle manifest v1 contract |

Reference: [../formats.md](../formats.md) holds the verified on-disk formats of each tool.

**Rules:**
- Contracts in M0.md change there first, then in code.
- If a task turns out to be larger than 4 h, split it in TASKS.md before starting.
