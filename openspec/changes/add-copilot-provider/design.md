## Context

Providers implement `provider.Provider` (`src/provider/provider.go`) and are registered in `cmd/dashboard/main.go`. The Claude provider (`src/provider/claude`) is the closest model: it parses JSONL files, caches them by mtime, and builds `SessionNode` trees.

What Copilot CLI writes to disk (checked against a local `~/.copilot`, CLI 1.0.x):

| Source | Content | Token data |
|---|---|---|
| `session-state/<id>/events.jsonl` | `session.start`, `user.message`, `assistant.message`, `tool.execution_*`, `subagent.started/completed`, `session.shutdown`, ... Each line has `type`, `id`, `parentId`, `timestamp`, `data`, and optionally `agentId` | `assistant.message.data.outputTokens` only; `subagent.completed.data.totalTokens`; `session.shutdown.data.modelMetrics[model].usage` with `inputTokens/outputTokens/cacheReadTokens/cacheWriteTokens/reasoningTokens` |
| `session-state/<id>/workspace.yaml` | flat `key: value`: `id, cwd, name, created_at, updated_at, ...` | — |
| `session-state/<id>/inuse.<pid>.lock` | present while a CLI process holds the session | — |
| `session-store.db` (SQLite) | `sessions`, `turns`, `assistant_usage_events(session_id, agent_id, parent_tool_call_id, model, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, reasoning_tokens, duration_ms, created_at, ...)` | full per-call breakdown, **but 0 rows on the reference machine** |

`session.shutdown` was found in only 2 of 69 sessions. In practice, most sessions will show output tokens only unless `assistant_usage_events` gets populated.

## Goals / Non-Goals

**Goals:**
- A `copilot` provider in its own package, at the same structure and test level as `claude`.
- Token numbers are correct or 0. They are never estimated (see spec "Token accounting without substitution").

**Non-Goals:**
- Showing `subagent.completed.totalTokens` or `session.shutdown.currentTokens` in the token columns. They might go in a separate UI field later, but that is not part of this change.
- Cost or premium-request (AIU) display.
- Runtime path switching (`PathSwitchable() == false`, same as claude).
- VS Code / JetBrains Copilot chat history (other formats), cloud session store.

## Decisions

1. **Package `src/provider/copilot`**, files: `provider.go` (interface wiring, root resolution), `events.go` (JSONL parse into a per-session model), `usage.go` (store DB reader + token source selection), `tree.go` (node building), plus tests with fixtures under `testdata/`.
   - Alternative: put it inside `claude` as a shared JSONL engine. Rejected because the event schemas don't share anything useful.

2. **Root = `COPILOT_HOME` or `~/.copilot`**. `Available()` checks `<root>/session-state` exists. `FatalWhenMissing()` returns true, consistent with claude.

3. **Event attribution by `agentId`.** Each event has an optional top-level `agentId`, which equals the `toolCallId` of the `task` call that started the subagent. Events without it belong to the root session. Child node ID = `<sessionId>/<agentId>`, which keeps it unique across sessions. The parent comes from the `agentId` on the `subagent.started` event itself. If it is empty, the parent is the root session; if it is set, the parent is that subagent, which handles nesting.

4. **Token source selection per node**, as the spec orders it:
   - Load `assistant_usage_events` once per tree build (read-only DSN, same `modernc.org/sqlite` driver as opencode), grouped by `(session_id, agent_id)`. If a group exists for the node, use it for both totals and calls.
   - Else, for root sessions, sum `modelMetrics[*].usage` over the session's `session.shutdown` events. Each event covers one CLI process run, and a resumed session gets a new run. If a fixture shows the counts are cumulative instead, take the last event only. That choice is local to `usage.go`.
   - Else, sum `outputTokens` from `assistant.message`. Calls API: one row per message, with `Out` set and the other fields 0.
   - Shutdown metrics are assumed to include subagent spend. Verify this against a real session that has both subagents and a shutdown event. If they turn out to be root-only, skip the subtraction below.
   - When shutdown metrics are the root's source, they already include subagent spend. The root node's own fields get the shutdown totals minus nothing; `Subtree` sums are still computed by the shared tree logic. To avoid double counting, subagent nodes under such a root use output-only tokens, and the shared `Subtree` sum would then add those outputs again. **Decision:** when shutdown totals are used, subtract the children's output sum from the root's own output so that subtree = shutdown totals. This is not substitution: no field gets another field's value.
   - `Ctx`/`MaxCtx` = `in + cr + cw` only when per-call rows exist; otherwise 0.

5. **Calls and trigger.** Message ID = `assistant.message.data.messageId`. If the DB rows have no message ID, they are paired with assistant messages by (agent, order). The trigger walks back through `parentId` / file order to the preceding `user.message` (text part) or `tool.execution_complete` events (tool parts: name, status, truncated input/output), reusing the truncation helpers in `src/provider/util.go`. `Hu` = true when the trigger is a user message.

6. **Prompt.** Root: `user.message.data.content` values in order. Subagent: the `prompt` argument from the `toolRequests` entry whose `toolCallId` equals the agent's ID.

7. **Caching.** Like claude, cache parsed `events.jsonl` by (path, mtime, size). Re-query the store DB on each `BuildTree`; it's indexed and small.

8. **workspace.yaml** is parsed with a minimal line-based `key: value` reader, so there's no YAML dependency. Unknown keys are ignored. On parse failure, fall back to `session.start.data.context.cwd` and event timestamps.

9. **Active** = an `inuse.*.lock` exists **and** the last event is less than 10 minutes old. Lock files can be left behind by a crashed process, so the timestamp check guards against that.

## Risks / Trade-offs

- [Undocumented, changing format] → Parse tolerantly. Unknown types and fields are ignored, and each file is parsed on its own so one bad file can't break the tree. Golden-file tests use sanitized real fixtures.
- [Most sessions show output-only tokens] → Documented in the README. Users see 0 for input/cache instead of a wrong number. That's an accepted trade-off of the no-substitution rule.
- [`assistant_usage_events` semantics not verified with real rows] → Code against the schema. Verify mapping (`agent_id` == subagent `toolCallId`, per-call granularity) as soon as a populated DB is available, using a hand-made fixture DB in tests until then.
- [Large `events.jsonl` (hooks/permissions events dominate)] → Stream line by line and decode only the event types we use. Rely on the mtime cache.
- [DB locked by a running CLI (WAL)] → Open read-only. If that fails, skip the DB source, log it once, and keep serving.
