# Tasks: add-claude-code-provider

## 1. Provider abstraction (behavior-preserving refactor)

- [ ] 1.1 Move existing SQL functions (`build_tree`, `session_calls`, `session_prompt`, `call_trigger`, and helpers) into an `opencode` provider namespace; add a `PROVIDERS` registry and startup selection via CLI flag `--provider` / env `PROVIDER` (default `opencode`). Verify: `python3 dashboard.py` starts and `/api/tree` returns identical output to pre-refactor (diff JSON).
- [ ] 1.2 Add `available()` check per provider at startup with a clear error naming the missing path. Verify: with `PROVIDER=claude` and `~/.claude/projects` absent (simulate with env override of the root), dashboard prints error and exits.

## 2. Claude JSONL provider

- [ ] 2.1 Implement JSONL scanning/indexing: discover `<slug>/<session-id>.jsonl`, parse lines with mtime-keyed cache, tolerate malformed lines. Verify: manual run against `~/.claude/projects` lists all sessions; a corrupted-line fixture is skipped without crash (unit test).
- [ ] 2.2 Implement message normalization: dedup assistant lines by `uuid`, skip zero-usage/synthetic, map usage fields, compute per-call `ctx`, `hu` flag, `ai-title` titles, `cwd` directory. Verify: unit test on a small fixture — totals equal sum of deduped usages; retried line counted once.
- [ ] 2.3 Implement subagent ingestion: read `<session-id>/subagents/agent-*.{jsonl,meta.json}`, attach as children via `toolUseId` with filesystem fallback, set `agent` from `agentType`, node `source: "claude"`. Verify: tree shows subagent children for the real `dev3-groups` session with nested data.
- [ ] 2.4 Implement `session_calls`, `session_prompt`, `call_trigger` equivalents over the normalized index (same JSON shapes as opencode provider). Verify: `/api/calls/<sid>` and `/api/trigger/<mid>` render sensibly in the UI for a real Claude session.

## 3. Integration & polish

- [ ] 3.1 Support `source` field on opencode nodes too (`"opencode"`), pass through to frontend unchanged. Verify: opencode tree nodes carry `source`.
- [ ] 3.2 Smoke-test both providers end-to-end with the dashboard UI (tree, sparkline beats, active window, prompts, calls, triggers). Verify: no console errors, `NO_BROWSER=1 PROVIDER=claude python3 dashboard.py` serves correct data.
