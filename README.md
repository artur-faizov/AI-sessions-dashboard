# OpenCode Token Usage Tree

Read-only web dashboard that visualizes token usage of all OpenCode sessions —
including every subagent, nested to any depth — as a tree.

## Why it exists

OpenCode runs each subagent as a real **child session**. All of them — with token counts,
model, cost, duration, and the prompt they were given — are persisted in a local SQLite
database (`~/.local/share/opencode/opencode.db`, table `session` linked by `parent_id`).
None of the built-in tools show this data well:

- `opencode web` — official web UI, shows sessions/chats but no per-agent token tree
- `/tokenscope` — great per-session report, but text-only and one session at a time
- TUI stats — totals only, no breakdown per subagent

This tool answers questions like:

- Which subagent consumed the most tokens / cost in a session?
- Was a session's spend its own context, or its subagents?
- What exact prompt was sent to a given subagent, and what model ran it?
- Total usage across all sessions, grouped by project

## How to use

```bash
python3 dashboard.py
```

Opens http://localhost:8787 in your browser.

- Custom port: `PORT=9000 python3 dashboard.py`
- Press `Ctrl+C` to stop

### Reading the tree

- Sessions are grouped by project directory; each root session nests its subagents
  (and their subagents) via `parent_id`
- Every row shows **subtree totals**: input/output tokens (with cache read), cost,
  and wall-clock duration of that session. Nodes with children also show their
  **own** input in brackets — `in 656k (own 336k)` — and the bar is two-tone:
  blue = own consumption, amber = subagents'
- A `+` / `−` toggle appears only on nodes that actually have children
- Click any row → side panel shows:
  - own vs children vs subtree tokens (input, output, reasoning, cache read/write)
  - agent, model, timestamps, duration, cost (own and subtree)
  - **every AI call in that session**: one table row per assistant message —
    context size in (fresh input + cache read), fresh input, cache read, out,
    reasoning; the Σ footer reconciles with the session's own-token counters
  - **click a call row** → popup with the message that triggered that call:
    the user prompt (if it was the first call of a turn) or the previous
    call's tool command + result. Long parts are truncated at 60k chars.
    System prompt / tool schemas are not stored in the DB and not shown
  - popup header shows **context now** (session's current context window) vs
    **this request** (size of the viewed call), plus fresh input and output
  - **‹ / › buttons** (and ← / → arrow keys) step through calls; the popup has a
    fixed size, so the buttons never move on screen — rapid clicking works
  - the **initial prompt sent to that session** (subagent's incoming request)
- Filter box matches title / agent / model; "live" checkbox re-reads data every 10s

### Interpreting numbers

- Big **input** on a leaf session (e.g. 600k+) means its own working context was large —
  many files/context fed to the model each step — not that it spawned subagents
- A session's input is **cumulative over all calls**: every turn re-sends the whole
  conversation, so N calls at ~85k context accumulate ~N×85k input (mostly cheap
  cache reads). The "in (context)" column in the AI-calls table is the size of that
  single call — comparable to opencode's context gauge
- Only rows with an expandable `+` and a `▾ N children` badge contain subagents
- `cost` comes from what OpenCode recorded per session; provider pricing gaps
  can make it `0.0` for some models

## Safety

- Opens the DB strictly **read-only** (`sqlite3` URI `mode=ro`) — safe to run while
  opencode is working; it never writes to the database
- The `-wal`/`-shm` file handles it holds are standard SQLite WAL reader coordination

## Requirements

- Python 3.8+ (stdlib only — no dependencies)
- OpenCode's local database at `~/.local/share/opencode/opencode.db`
  (path is hardcoded at the top of `dashboard.py` if yours lives elsewhere)

## Files

- `dashboard.py` — server: read-only DB access, tree building, JSON API
- `index.html` — the UI (HTML/CSS/JS), served per request — edit it and
  just refresh the browser, no server restart needed
