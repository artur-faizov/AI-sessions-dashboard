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
make mac-app     # macOS: dist/TokenMonitor.app (Dock/Finder launcher)
make mac-app-install  # macOS: also copy it to ~/Applications
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
- Dock launcher (macOS): run `make mac-app-install`, open `~/Applications`
  in Finder and drag `TokenMonitor.app` onto the Dock. Clicking it starts
  the dashboard and opens it in your browser; logs go to
  `~/Library/Logs/TokenMonitor.log`. Clicking the icon again reopens the
  page; right-click → Quit stops the dashboard. Re-run `make mac-app-install` after rebuilding to update the
  copy in `~/Applications`
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
- The sticky header labels every column: `EKG` · `#` · `session` · `AI model` ·
  `dur` · `ctx` · `spent`
- Click `#` / `dur` / `ctx` / `spent` in the sticky header to sort siblings at every
  level of the tree — click cycles descending → ascending → off (newest first)
- Keyboard: `↑` / `↓` (and `Home` / `End`) move the selection through visible rows,
  `→` / `←` expand / collapse (or jump to the parent), `Enter` toggles children
- The selected session is kept in the URL (`#session=<id>`): links open straight to
  it (collapsed ancestors are expanded), and browser Back / Forward walk the
  sessions you clicked
- Subagent titles drop opencode's `(@agent subagent)` suffix when the agent badge
  already shows it (the full title is on hover)
- Click any row → side panel shows:
  - a token grid: own / children / subtree × in, out, reason, cache r, cache w
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
  - calls where the **AI asked you a question** (opencode `question`, Claude
    `AskUserQuestion`, Copilot `ask_user` tool) are highlighted purple with a
    speech-bubble ❓ icon; for Claude Code and opencode, so is the last call of a
    turn (right before your next message) whose text asks a question — a
    sentence-ending `?` anywhere outside code blocks, inline code and URLs
  - a color legend under the calls table explains the row highlights (your
    input, AI question, context warn / crit thresholds)
  - **click a call row** → popup with the message that triggered that call:
    the user prompt (if it was the first call of a turn) or the previous
    call's tool command + result; for a purple (AI question) row the AI's
    reply in that call (its text and the question asked) comes first. Long
    parts are truncated at 60k chars.
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
  is available for the AI-calls table via its own Filter button. A Filter button
  is highlighted only while a filter is set and shows how many criteria are active
  (`Filter · 2`); the dialog header shows a live "N of M shown" count, and the
  model / provider lists have `all` / `none` shortcuts

### Attention alerts

For the Claude Code and opencode providers the dashboard tells you when an agent
needs you. Within one refresh (5 s) of the moment a session

- **asks you a question** (question tool, plan approval) — `ask` sound, amber blinking row
- **waits for a tool permission** — `perm` sound, red blinking row
- **finished its job** — the root session and all its subagents went idle —
  `done` sound, green blinking row

a sound plays once and the row blinks until you click it (a collapsed parent
blinks for a session hidden inside it). The mark also goes away by itself once
its reason is gone — you answered in the terminal, or a new turn started. The
two header buttons (speaker, blink) turn sound and blinking off; both are on by
default and remembered in the browser. Browsers allow sound only after you have
interacted with the page: until then the speaker button turns amber.
In settings (gear) → **alert sound** pick the sound from a list, softest at the
top: the original chimes (default) or one of 16 sounds synthesized in the
browser, from a quiet whisper to an alarm beep. Picking one plays a sample. The
**volume** slider below it sets the loudness for any sound (default 60%).

How the state is read, without any hooks or plugins:

- **Claude Code** keeps a live status per running process in
  `~/.claude/sessions/<pid>.json` (`busy` / `idle` / `waiting`); a pending
  `AskUserQuestion` / `ExitPlanMode` in the transcript tells a question from a
  permission prompt. This file is Claude Code internals and may change between versions
- **opencode** does not store pending permission prompts, so they are inferred:
  a running `bash` tool with no child process started since it began, or a fast
  tool (`edit`, `write`, `read`, …) still running after 3 s. A pending `question`
  tool and finished turns come straight from the database

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
The current order is OpenCode, Claude Code, Copilot CLI, folder, and Codex; OpenCode
remains the default.

### Codex provider

`--provider codex` (or `PROVIDER=codex`, or the UI switcher) reads local Codex CLI,
IDE, and desktop rollouts from `CODEX_HOME` (default `~/.codex`), read-only:

```sh
go run ./cmd/dashboard --provider codex
CODEX_HOME=/path/to/.codex ./dist/dashboard-lin-x64 --provider codex
```

- sessions are read from `sessions/**/*.jsonl`; `archived_sessions/**/*.jsonl` is
  included when present. Startup requires either directory to exist.
- child agents are shown beneath their parent when the rollout identifies the link.
- token usage comes from local rollout records. Codex's cached input is part of its
  input total, and reasoning is part of output; the dashboard separates these into
  additive buckets, so adding all displayed buckets does not double-count either.
- context size is the observed input size for a call, not the model's context
  capacity. Exact prompt composition is not available in the rollout data.
- active status is a heuristic based on task lifecycle and recent source timestamps;
  stale or ambiguous activity may not match the actual process state.
- cloud-only sessions that have no local rollout are outside this provider's scope.

Codex's rollout JSONL is an internal format and may change between Codex versions.
The provider does not read credentials, write Codex files, require telemetry setup,
or depend on Codex's internal database schema.

### Copilot CLI provider

`--provider copilot` (or `PROVIDER=copilot`, or the UI switcher) reads GitHub Copilot
CLI data from `COPILOT_HOME` (default `~/.copilot`), read-only:

```sh
go run ./cmd/dashboard --provider copilot
COPILOT_HOME=/path/to/.copilot ./dist/dashboard-lin-x64 --provider copilot
```

- sessions come from `session-state/<id>/events.jsonl` + `workspace.yaml`; startup fails
  with the missing path if `<COPILOT_HOME>/session-state` does not exist
- subagents started via the `task` tool become child nodes (nested subagents nest too)
- a session is active while an `inuse.*.lock` file exists and its last event is under
  10 minutes old

Token coverage is limited by what Copilot writes to disk. Each node takes its
numbers from the first source available, and a field is never estimated or derived
from another field (the one exception is export input, below). Fields with no source
show 0:

1. the OpenTelemetry file export (see below), when a span matches the node.
2. per-call rows in `session-store.db` (`assistant_usage_events`): all fields, plus
   context size. These are used when present (often empty in practice).
3. root sessions only: `session.shutdown` model metrics (written on a clean exit).
   These give input (fresh, cache excluded), output, cache read/write and reasoning,
   as session totals that already include subagent spend.
4. otherwise **output tokens only** (per-message `outputTokens`); input, cache and
   reasoning stay 0. `subagent.completed.totalTokens` is never shown as a token field.

#### Full token data via the OpenTelemetry file export

Copilot (CLI and the IDE language server, verified with 1.0.63) writes exact per-call
usage to a JSON-lines file when `COPILOT_OTEL_FILE_EXPORTER_PATH` is set in the
environment of the Copilot process. There is no config-file switch for it, so the
variable has to reach every place Copilot is started from. One command does that:

```sh
make copilot-otel-enable    # set COPILOT_OTEL_FILE_EXPORTER_PATH everywhere (idempotent)
make copilot-otel-status    # show what is set and which running Copilot processes export
make copilot-otel-disable   # undo the managed changes (export files are kept)
# custom file (must be absolute; keep it in ~/.copilot/otel/ so the dashboard finds it):
make copilot-otel-enable COPILOT_OTEL_PATH=$HOME/.copilot/otel/work.jsonl
```

`copilot-otel-enable` (`scripts/copilot-otel.sh`) points the export to
`<COPILOT_HOME>/otel/copilot-otel.jsonl` and sets it for:

- login shells (Copilot CLI): a marked block in `~/.profile`, plus `~/.bash_profile` if it
  exists and `~/.zprofile` if your shell is zsh. A profile that already sets the variable
  by hand is left alone.
- the Linux desktop session: `~/.config/environment.d/60-copilot-otel.conf`, and the
  running systemd/D-Bus user environment, so apps started from the menu get it.
- JetBrains launchers (`jetbrains-*.desktop`, including Toolbox): `Exec=` lines are
  prefixed with `env COPILOT_OTEL_FILE_EXPORTER_PATH=...`. System, snap, and flatpak
  launchers are copied to `~/.local/share/applications/` first. Toolbox may regenerate
  its launchers on IDE updates; re-run the command after an update.
- macOS: `launchctl setenv` for apps started from the Dock (until logout); shells get it
  from the profile.

Then fully quit and restart the Copilot CLI and your IDEs. `copilot-otel-status` lists
any running `copilot` / `copilot-language-server` process that still lacks the variable.
Sessions recorded before the restart stay output-only. On Windows, set the variable
yourself, e.g. `setx COPILOT_OTEL_FILE_EXPORTER_PATH %USERPROFILE%\.copilot\otel\copilot-otel.jsonl`.

The dashboard reads the file named by `COPILOT_OTEL_FILE_EXPORTER_PATH` (as seen by the
dashboard) plus every `<COPILOT_HOME>/otel/*.jsonl`, read-only and incrementally; a
file reached both ways is read once and repeated spans count once. Spans are matched
to messages by session id and `apiCallId`. With the export:

- main-agent calls get input, cache read, cache write, output, reasoning, and context
  size (`ctx`); the session node gets their sums and `maxctx` (largest `ctx`). A call
  without a span keeps output only. Input is the new tokens only: the exported
  `input_tokens` minus cache read minus cache write; `ctx` is `input_tokens` as reported.
- subagent nodes get exact input, cache, output, and reasoning totals (Copilot exports
  one span per subagent run, written when it finishes), but `maxctx` stays 0 and their
  calls show output only — Copilot does not export per-call numbers for subagents.
- shutdown metrics and `session-store.db` rows are ignored for a node that has spans.

The file grows by roughly 8 KB per model call and is never rotated by Copilot. To
rotate it, move it to another `*.jsonl` name inside `~/.copilot/otel/` (it keeps
counting) or delete old files (their sessions fall back to the sources above).
Invalid, partial, or metric lines are skipped.

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
  (`opencode.New(...)`, `claude.New()`, `copilot.New()`, `folder.New()`, `codex.New()`;
  the first is the default) and calls `dashboard.Run`
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
- `src/provider/copilot/` — Copilot CLI provider: parses `~/.copilot/session-state`
  event logs (incl. nested subagents), `session-store.db` usage rows, and the
  OpenTelemetry file export (`otel.go`)
- `src/provider/codex/` — Codex provider: parses local CLI, IDE, and desktop rollout
  JSONL files under `CODEX_HOME` (including archived sessions when present)
- `scripts/copilot-otel.sh` — enables/checks/disables the Copilot OpenTelemetry export
  (`make copilot-otel-enable|status|disable`)
- `src/dashboard/assets.go` — serves the frontend: `{{VERSION}}` substitution
  and dev-mode disk reads for `src/index.html` / `src/i18n.js`
- `src/dashboard/browser.go` — opens the dashboard in the default browser
- `src/embed.go` — go:embed of the frontend (`src/index.html`, `src/i18n.js`, `src/sounds/`)
- `src/sounds/` — alert sounds from the freedesktop sound theme (see `src/sounds/CREDITS`)
- `go.mod` / `go.sum` — Go module (modernc.org/sqlite pure-Go SQLite
  driver — no CGO)
- `src/index.html` — the UI (HTML/CSS/JS), embedded into the binary via
  go:embed; edit-and-refresh via `make dev` / `go run ./cmd/dashboard -dev`, which
  serves the assets from `src/` on disk — no server restart needed
- `test/*.test.mjs` — frontend tests;
  `make test` = `go test ./...` + `node --test` over them
- `Makefile` — `make build` / `make build-all` (standalone binaries),
  `make test`, `make dev`, `make clean`, `make copilot-otel-enable|status|disable`
