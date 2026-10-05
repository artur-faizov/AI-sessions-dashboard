# OpenCode Token Usage Tree

Read-only web dashboard that visualizes token usage of all OpenCode sessions —
including every subagent, nested to any depth — as a tree.

## Why it exists

OpenCode runs each subagent as a real **child session**. All of them — with token counts,
model, duration, and the prompt they were given — are persisted in a local SQLite
database (`~/.local/share/opencode/opencode.db`, table `session` linked by `parent_id`).
None of the built-in tools show this data well:

- `opencode web` — official web UI, shows sessions/chats but no per-agent token tree
- `/tokenscope` — great per-session report, but text-only and one session at a time
- TUI stats — totals only, no breakdown per subagent

This tool answers questions like:

- Which subagent consumed the most tokens in a session?
- Was a session's spend its own context, or its subagents?
- What exact prompt was sent to a given subagent, and what model ran it?
- Total usage across all sessions, grouped by project

## How to use

```bash
go run ./cmd/dashboard
```

(or run a prebuilt binary from `dist/` — see "Standalone executable" below)

Opens http://localhost:8787 in your browser.

- Custom port: `PORT=9000 go run ./cmd/dashboard` — if the port is busy, it
  automatically tries the next ports (8788, 8789, …) and says so
- Custom database: `go run ./cmd/dashboard --db /path/to/opencode.db` (or `DB_PATH=/path/to/opencode.db go run ./cmd/dashboard`)
- Show version: `go run ./cmd/dashboard --version` (also displayed in the page header)
- Press `Ctrl+C` to stop

### Database location

The dashboard auto-detects the DB — in priority order: `--db` flag → `DB_PATH` env →
`~/.local/share/opencode/opencode.db` (Linux) → `~/Library/Application Support/opencode/opencode.db`
(macOS) → `%APPDATA%`/`%LOCALAPPDATA%\opencode\opencode.db` (Windows).

If it can't be found, the dashboard **still opens** and shows a DB path dropdown
in the top-center of the page header, highlighted with a red border: pick one of
the autodetected candidates or choose "Custom…" and enter the path to your
`opencode.db` (validated read-only before switching). Your choice is remembered
in the browser and re-applied on next launch; if the remembered path goes stale,
the dashboard silently falls back to autodetection.

### Standalone executable

Compile a self-contained binary — pure Go, so nothing beyond the Go
toolchain is needed at build time, and the file in `dist/` runs on its
own, needing nothing installed:

```bash
make build       # binary for the host platform
make build-all   # all five targets, cross-compiled from any host OS
```

- Output (`build-all`): `dist/dashboard-lin-x64` (linux/amd64),
  `dist/dashboard-lin-arm64` (linux/arm64), `dist/dashboard-mac-darwin`
  (darwin/arm64), `dist/dashboard-mac-darwin-amd64` (darwin/amd64),
  `dist/dashboard.exe` (windows/amd64)
- Double-click it (or run from a terminal) — the dashboard opens in your
  browser, identical to `go run ./cmd/dashboard`
- The binary is unsigned, so first run trips the OS warning:
  macOS Gatekeeper — right-click → Open, or
  `xattr -d com.apple.quarantine <file>`; Windows SmartScreen —
  "More info" → "Run anyway"
- `make clean` removes build artifacts (`dist/`, `build/`)

### Reading the tree

- Sessions are grouped by project directory; each root session nests its subagents
  (and their subagents) via `parent_id`
- The name takes all remaining space; to its right sits a compact fixed-width set:
  **no.** (position in history — roots numbered globally by start time, children
  within their parent; full timestamp on hover) ·
  wall-clock **duration** · **max context size reached during that session** (yellow) ·
  **tokens spent** (subtree `in + out + reasoning`, cache excluded as it's cheap)
- A `+` / `−` toggle appears only on nodes that actually have children
- Click `time` / `dur` / `ctx` / `spent` in the sticky header to sort siblings at every
  level of the tree — click cycles descending → ascending → off (newest first)
- Click any row → side panel shows:
  - own vs children vs subtree tokens (input, output, reasoning, cache read/write)
  - agent, model (provider / model / variant), timestamps, duration, max context
  - **every AI call in that session**: one table row per assistant message —
    hover any column header for its meaning. Two groups: **context growth** —
    `in (context)` (total input of the request = fresh + cache read + cache
    write), `delta` (context growth vs the previous call ≈ trigger/tool
    results fed in + previous call's output and reasoning; negative = context
    shrank through compaction), `out`, `reason`; then (separated by a border)
    **input billing** — `fresh` (input tokens billed at full price, uncached)
    and `cache r` (~10x cheaper cached input). The Σ footer reconciles with
    the session's own-token counters; Σ delta = final context size
  - rows triggered by a **human message** (your typed input — not tool
    results or synthetic context) are highlighted green with a 👤 icon
  - **click a call row** → popup with the message that triggered that call:
    the user prompt (if it was the first call of a turn) or the previous
    call's tool command + result. Long parts are truncated at 60k chars.
    System prompt / tool schemas are not stored in the DB and not shown
  - popup header shows **context now** (session's current context window) vs
    **this request** (size of the viewed call), plus delta (growth vs the
    previous call), fresh (uncached) input and output
  - **‹ / › buttons** (and ← / → arrow keys) step through calls; the popup has a
    fixed size, so the buttons never move on screen — rapid clicking works
  - the **initial prompt sent to that session** (subagent's incoming request)
- The **Filter** button opens the session filter: text (title / agent / model),
  model and provider checkboxes, and inclusive ranges — order number (`#`),
  max context (k tokens), tokens spent (k tokens), duration. The **Created date**
  section narrows by session start: presets Today / This week (from Monday) /
  This month, or Custom `from` / `to` dates (inclusive, local timezone — presets
  re-resolve on each refresh, so "Today" survives midnight). A session stays
  visible while it or any descendant matches; the same pattern (per-column ranges)
  is available for the AI-calls table via its own Filter button

### Interpreting numbers

- Tree rows show an **agent badge** (e.g. `explore`, `general`, `vision`, `plan`) for
  every agent except opencode's default (`build`) — the default would be a constant
  on ~every session, so it is hidden. The agent is always visible in the detail panel
- Big **input** on a leaf session (e.g. 600k+) means its own working context was large —
  many files/context fed to the model each step — not that it spawned subagents
- A session's input is **cumulative over all calls**: every turn re-sends the whole
  conversation, so N calls at ~85k context accumulate ~N×85k input (mostly cheap
  cache reads). The "in (context)" column in the AI-calls table is the size of that
  single call — comparable to opencode's context gauge
- A `fresh` spike with `cache r = 0` in the AI-calls table is a **cache miss**: the
  provider's prompt cache didn't hit (expired after an idle gap or invalidated), so
  the whole context was reprocessed at full input price — it is not the size of the
  new trigger text (that is roughly the `delta` column)
- Only rows with an expandable `+` and a `▾ N children` badge contain subagents

## Data providers

Each data source lives in its own package under `src/provider/<name>` and implements
`provider.Provider` (`src/provider/provider.go`). The dashboard shell (`src/dashboard`)
never imports a provider package and never branches on a provider's name — anything
provider-specific is a capability on the interface, e.g. `PathSwitchable()` drives the
`switchable` flag of `GET /api/db`, which in turn shows or hides the DB picker in the UI.

Select a provider with `--provider NAME` or `PROVIDER=NAME`; without one, the first
provider registered in `main.go` is used (no automatic fallback when its data is missing).

To add a provider:

1. Create `src/provider/<name>/` with a `New()` constructor returning a type that
   implements `provider.Provider` (import only `src/provider`, never another provider
   or `src/dashboard`); keep its tests in that package.
2. Add one entry to the provider list in `cmd/dashboard/main.go`.

## Safety

- Opens the DB strictly **read-only** (`sqlite3` URI `mode=ro`) — safe to run while
  opencode is working; it never writes to the database
- The `-wal`/`-shm` file handles it holds are standard SQLite WAL reader coordination

## Requirements

- Go 1.26+ (only to build from source — the compiled binaries are
  self-contained and need nothing on the target machine)
- Node.js — for the frontend tests (`make test`)
- OpenCode's local database — auto-detected (see "Database location" above),
  or set the path via `--db` / `DB_PATH` or the in-page field

## Files

- `cmd/dashboard/main.go` — entrypoint: builds the ordered provider list
  (`opencode.New(...)`, `claude.New()`; the first is the default) and calls `dashboard.Run`
- `src/dashboard/run.go` — startup: `--version`, `--provider` and `-dev` flags, provider
  selection, port fallback, Ctrl-C graceful shutdown
- `src/dashboard/server.go` — HTTP server: routing, JSON helpers, `/` and `/i18n.js` handlers;
  talks to the active data source only through the `provider.Provider` interface
- `src/provider/` — the provider contract (`Provider` interface), shared payload types
  (`SessionNode`, `CallRec`, `TriggerResp`; JSON shape pinned by golden tests) and helpers
- `src/provider/opencode/` — opencode sqlite provider: DB auto-detection, `--db` parsing,
  validation, runtime-switchable read-only pool, session tree, per-call usage, triggers
- `src/provider/claude/` — Claude Code provider: parses `~/.claude/projects` JSONL
  transcripts (incl. subagents) into the same payloads
- `src/dashboard/assets.go` — serves the frontend: `{{VERSION}}` substitution
  and dev-mode disk reads for `src/index.html` / `src/i18n.js`
- `src/dashboard/browser.go` — opens the dashboard in the default browser
- `src/embed.go` — go:embed of the frontend (`src/index.html`, `src/i18n.js`)
- `go.mod` / `go.sum` — Go module (modernc.org/sqlite pure-Go SQLite
  driver — no CGO)
- `src/index.html` — the UI (HTML/CSS/JS), embedded into the binary via
  go:embed; edit-and-refresh via `make dev` / `go run ./cmd/dashboard -dev`, which
  serves the assets from `src/` on disk — no server restart needed
- `test/*.test.mjs` — frontend tests;
  `make test` = `go test ./...` + `node --test` over them
- `Makefile` — `make build` / `make build-all` (standalone binaries),
  `make test`, `make dev`, `make clean`
