# Tasks: add-claude-code-provider

## 1. Provider abstraction (behavior-preserving refactor)

- [x] 1.1 Move the existing SQL query functions behind a provider interface in `src/dashboard` (method set `buildTree`, `sessionCalls`, `sessionPrompt`, `callTrigger`, plus `available()`); add a `PROVIDERS` registry and startup selection via CLI flag `--provider` / `--provider=NAME` / env `PROVIDER` (default `opencode`); server handlers call through the active provider. Verify: `go test ./...` green and `/api/tree` JSON identical to pre-refactor (run the server against the real opencode DB before and after, curl `/api/tree`, diff).
- [x] 1.2 Add `available()` per provider: opencode keeps its lenient missing-DB behavior (still serves, dropdown picks a path); claude exits with a clear error naming the missing root path; claude root overridable via env `CLAUDE_PROJECTS_DIR`. Verify: `NO_BROWSER=1 PROVIDER=claude CLAUDE_PROJECTS_DIR=/nonexistent go run ./cmd/dashboard` prints the error naming the path and exits non-zero (unit test the availability function too).

## 2. Claude JSONL provider

- [x] 2.1 Implement JSONL scanning/indexing: discover `~/.claude/projects/<slug>/<session-id>.jsonl`, parse lines with an mtime-keyed cache, tolerate malformed lines. Verify: unit test with a corrupted-line fixture skips bad lines without crashing; manual run against real `~/.claude/projects` lists all sessions.
- [x] 2.2 Implement message normalization: dedup assistant lines by `uuid`, skip zero-usage/synthetic, map usage fields (`input_tokens`→in, `output_tokens`→out, `cache_read`→cr, `cache_creation`→cw, reason=0), compute per-call `ctx`, `hu` flag, `ai-title` titles, `cwd` directory. Verify: unit test on a small fixture — session totals equal the sum of deduped usages; a retried (duplicate-uuid) line is counted once.
- [x] 2.3 Implement subagent ingestion: read `<session-id>/subagents/agent-*.{jsonl,meta.json}`, attach as children via `toolUseId` with filesystem fallback, set `agent` from `agentType`, node `source: "claude"`. Verify: unit test; tree shows subagent children for the real `dev3-groups` session with nested data.
- [x] 2.4 Implement `sessionCalls`, `sessionPrompt`, `callTrigger` equivalents over the normalized index (same JSON shapes as the opencode provider). Verify: unit tests with fixtures; `/api/calls/<sid>` and `/api/trigger/<mid>` render sensibly in the UI for a real Claude session.

## 3. Integration & polish

- [x] 3.1 Support a `source` field on opencode nodes too (`"opencode"`), passed through to the frontend unchanged. Verify: unit test — opencode tree nodes carry `source`.
- [x] 3.2 Smoke-test both providers end-to-end with the dashboard UI (tree, sparkline beats, active window, prompts, calls, triggers). Verify: no console errors; `NO_BROWSER=1 PROVIDER=claude go run ./cmd/dashboard` serves correct data; default run still serves opencode data.
