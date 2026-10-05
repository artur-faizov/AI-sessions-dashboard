# Design: add-claude-code-provider

## Context

The dashboard is a Go program (not Python as originally drafted): `cmd/dashboard` calls `dashboard.Run`; the `src/dashboard` package contains `server.go` (HTTP handlers calling query functions directly), `tree.go` (`buildTree`, `sessionBeats`, `sessionMaxContexts` over the SQLite pool), `calls.go` (`sessionCalls`, `sessionPrompt`, `callTrigger`), and `db.go` (runtime-switchable read-only pool `dbx`, `--db` flag / `DB_PATH` env, autodetected path candidates, and `/api/db` GET/POST endpoints backing the frontend DB-path dropdown). Tests: `src/dashboard/*_test.go` (Go) plus `test/*.mjs` (Node); `make test` runs both. The SQL functions issue queries against `~/.local/share/opencode/opencode.db`. Claude Code instead stores JSONL transcripts under `~/.claude/projects/<slug>/`, with subagents in `<session-id>/subagents/agent-*.jsonl` + `meta.json` (verified against real data on this machine). Verified line schema: assistant lines carry `message.usage` (`input_tokens`, `output_tokens`, `cache_read_input_tokens`, `cache_creation_input_tokens`, plus non-portable extras), `message.model`, `uuid`, `timestamp` (ISO-8601 string), `cwd`; titles come from `{"type":"ai-title","aiTitle":...}` lines; user tool_results carry `content[].{type:"tool_result",tool_use_id}`; parent Task/Agent tool_use blocks carry `content[].{type:"tool_use",id,name}`; subagent metas carry `agentType`, `toolUseId`, `spawnDepth`; subagent JSONL has the same line format with `isSidechain: true`.

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

### D1: Provider as a Go interface selected once at startup
Each provider implements `type provider interface { buildTree(nowMS int64) ([]*sessionNode, error); sessionCalls(sid string) ([]callRec, error); sessionPrompt(sid string) ([]string, error); callTrigger(mid string) (*triggerResp, error); available() error }` in the `dashboard` package. A registry `map[string]provider` holds `{"opencode": ..., "claude": ...}`; an `activeProvider` variable is set in `Run` from the CLI flag `--provider` (or `--provider=NAME`, mirroring `--db`) or env `PROVIDER`, default `opencode`; an unknown name is a startup error listing valid names. The HTTP handlers in `server.go` change only to call through `activeProvider` instead of the package-level functions.
*Why an interface rather than a plain map of function values:* the claude provider carries its own state (root dir, index cache), and the interface keeps the opencode side a thin wrapper over the existing SQL functions — the diff stays mechanical.

**Provider availability semantics:** the spec's "Missing data source → error and exit" scenario applies to the **claude** provider: at startup, if its root (`~/.claude/projects`, overridable for testing via env `CLAUDE_PROJECTS_DIR`) does not exist, print an error naming the path and exit non-zero. The **opencode** provider keeps its current lenient behavior exactly (missing DB still serves, user picks a path via the header dropdown), preserving the spec's "Default provider unchanged" scenario.

**`/api/db` endpoints:** they remain opencode-scoped. Under the claude provider, GET `/api/db` returns `{path: <claude root>, found: <dir exists>, error?, candidates: []}` (the frontend tolerates this shape; the dropdown shows the root path) and POST rejects with 400 ("path switching is only supported for the opencode provider"). The 503 error key for a missing data source stays `"db_not_found"` for both providers so the frontend's existing handling keeps working.

### D2: Claude provider indexes JSONL into memory per request, with mtime-keyed caching
On `/api/tree`, scan `~/.claude/projects/` once; parse each session file; keep a cached index (Go: `map[string]cachedFile{mtime, parsed}` guarded by a mutex) so `/api/calls/<sid>` and `/api/trigger/<mid>` don't re-read unrelated files. Sessions are small (hundreds of lines).
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

Refactor is behavior-preserving for the default provider: after extracting the SQL functions into the opencode provider, `/api/tree` output for opencode must be byte-identical (verify with `go test ./...` plus a manual curl diff of `/api/tree` against the real DB before and after the refactor). Rollback = revert; no data written.

## Open Questions

- Should `reason` display "—"-style in UI when source is claude, rather than 0? (UI polish, deferrable)
- Multi-provider combined view: defer to a future change.
