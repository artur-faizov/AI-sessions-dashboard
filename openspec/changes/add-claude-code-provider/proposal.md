# Proposal: add-claude-code-provider

## Why

The dashboard currently only reads opencode's SQLite DB, so sessions run in Claude Code are invisible. The user runs both tools; a single dashboard should show token usage and the subagent tree for both.

## What Changes

- Introduce a provider abstraction over the data source, with two implementations:
  - `opencode` (existing SQLite reader, unchanged behavior)
  - `claude` (new JSONL reader over `~/.claude/projects/`)
- The Claude provider scans main session transcripts plus `<session-id>/subagents/agent-*.jsonl` (+ `meta.json`) and reconstructs the subagent tree via `toolUseId` / `spawnDepth`.
- Token mapping: `input_tokens`, `output_tokens`, `cache_read_input_tokens`, `cache_creation_input_tokens`; reasoning tokens not available (0).
- Dedup duplicate assistant lines by `uuid`; skip synthetic messages (`model: "<synthetic>"`, zero usage).
- The `/api/*` endpoints return the same shapes for both providers; each node gains a `source` marker (`opencode` / `claude`).
- CLI flag / env var to select the provider (default: opencode, unchanged).

Non-goals: merging both providers into one combined tree in this change; writing any data; supporting Claude Code's legacy inline-sidechain format (`isSidechain` lines) beyond best-effort.

## Capabilities

### New Capabilities
- `claude-code-provider`: Read-only ingestion of Claude Code JSONL transcripts (sessions, subagents, per-call usage) exposed through the existing dashboard API shapes.

### Modified Capabilities
(none — no existing specs)

## Impact

- `dashboard.py`: refactoring SQL query functions behind a provider interface; new JSONL scanning code; startup DB check becomes provider-specific.
- Frontend (`src/index.html`): no structural change expected; may show the `source` badge.
- No new dependencies; stdlib only.
