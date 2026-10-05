# Design: add-claude-code-provider

## Context

Today `dashboard.py` reads opencode's SQLite DB directly via module-level functions (`build_tree`, `session_calls`, `session_prompt`, `call_trigger`, `session_beats`, `session_max_contexts`), each issuing SQL against `~/.local/share/opencode/opencode.db`. Claude Code instead stores JSONL transcripts under `~/.claude/projects/<slug>/`, with subagents in `<session-id>/subagents/agent-*.jsonl` + `meta.json` (verified against real data on this machine). Verified line schema: assistant lines carry `message.usage` (`input_tokens`, `output_tokens`, `cache_read_input_tokens`, `cache_creation_input_tokens`), `message.model`, `uuid`, `timestamp`, `cwd`; titles come from `ai-title` event lines; subagent metas carry `agentType`, `toolUseId`, `spawnDepth`.

## Goals / Non-Goals

**Goals:**
- Same dashboard UI and `/api/*` shapes for both providers
- Subagent tree for Claude Code matching the opencode `parent_id` tree semantics
- Read-only, stdlib-only, no frontend redesign

**Non-Goals:**
- Combined multi-provider tree in one view
- Legacy `isSidechain` inline subagents as tree children (flat fallback only)
- Reasoning-token split (Claude Code doesn't expose it)

## Decisions

### D1: Provider as a module-level strategy, not a class hierarchy
Each provider implements the same function set: `build_tree(now_ms)`, `session_calls(sid)`, `session_prompt(sid)`, `call_trigger(mid)`, plus `available()`. A `PROVIDER` global is set once at startup from CLI/env; HTTP handler functions stay as-is and call through the strategy.
*Why not classes:* the codebase is a single procedural script; a registry dict `{"opencode": ..., "claude": ...}` of functions fits better and keeps the diff mechanical. Alternative (full adapter classes with shared ABC) adds ceremony without benefit at this size.

### D2: Claude provider indexes JSONL into memory per request, with mtime-keyed caching
On `/api/tree`, scan `~/.claude/projects/` once; parse each session file; keep a cached index `{file_path: (mtime, parsed_sessions, messages_by_session)}` so `/api/calls/<sid>` and `/api/trigger/<mid>` don't re-read unrelated files. Sessions are small (hundreds of lines).
*Alternative considered:* copying into a SQLite mirror DB — rejected: extra write path, staleness, violates read-only simplicity.

### D3: IDs and tree shape
- Session id = `<claude-session-id>` (UUID, collides-free with opencode ids).
- Subagent node id = `agent-<hash>` from the filename; parent = the owning session (single-level; deeper nesting via `spawnDepth` later if real data needs it — current data shows `spawnDepth: 1` only).
- Parent matching: collect Task tool_use ids from parent transcript; map `meta.json.toolUseId` → parent session. Fallback: subagent belongs to the session whose directory it nests under (filesystem already encodes parentage), so the fallback is trivially correct.

### D4: Message model normalization
Normalize Claude lines into the opencode-shaped records the API functions already emit:
- assistant call: `{mid: uuid, time: timestamp_ms, in/out/cr/cw, ctx: in+cr+cw, reason: 0, hu}`
- dedup by `uuid`, keep first occurrence; skip zero-usage and `model == "<synthetic>"`
- `hu` (human-triggered): previous user message content is a real string (not tool_result, not `<system-reminder>` / command wrappers)
- trigger parts: user string → text part; tool_use blocks → tool parts (input only, no output available pre-assistant)
- `ai-title` lines provide title; `agentType` provides agent name; `cwd`/slug provides directory

### D5: Active-window and beats
`updated` = last line timestamp per session; active = within 15-min window, same as today. `beats` = timestamps of deduped assistant calls with nonzero usage. `maxctx` = max ctx over calls.

## Risks / Trade-offs

- [Claude Code changes its JSONL format between versions] → Parser is line-type driven and skips unknown types; zero-usage filtering already tolerates synthetic/error lines (`isApiErrorMessage` seen in data).
- [Large histories make first `/api/tree` slow] → Index cache + per-file lazy parse; only directories with recent mtime could be scanned if it becomes a problem (deferred).
- [Subagent orphaned if meta.json missing] → Fallback to filesystem parentage (D3) covers it.
- [Shared handler assumes provider always available] → Startup check: provider's `available()`; clear error message naming the missing path (spec requirement).

## Migration Plan

Refactor is behavior-preserving for the default provider: after extracting the SQL functions into the opencode strategy, `/api/tree` output for opencode must be byte-identical (verify manually before adding the claude provider). Rollback = revert; no data written.

## Open Questions

- Should `reason` display "—"-style in UI when source is claude, rather than 0? (UI polish, deferrable)
- Multi-provider combined view: defer to a future change.
