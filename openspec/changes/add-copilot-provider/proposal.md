## Why

GitHub Copilot CLI is now used next to opencode and Claude Code, but its token spend (including subagents launched via the `task` tool) is invisible in the dashboard. The provider refactor (`src/provider`, `provider.Provider`) makes a third data source a self-contained addition.

## What Changes

- Add a `copilot` provider package (`src/provider/copilot`) implementing `provider.Provider` from Copilot CLI's local data in `~/.copilot/`:
  - sessions from `session-state/<id>/events.jsonl` + `workspace.yaml`
  - subagents (`subagent.started` / `subagent.completed`, events tagged with `agentId`) as child session nodes
  - token usage from whichever **real** source carries each field: per-call rows in `session-store.db` table `assistant_usage_events`, `session.shutdown` per-model metrics, and per-message `outputTokens`
- Strict "no substitution" token rule: a field is filled only from a source that reports that exact field. Missing input/cache/reasoning values stay 0; values are never derived or re-labelled (e.g. `subagent.completed.totalTokens` is never put into input, input is never computed as total − output).
- Register the provider in `cmd/dashboard/main.go` (after opencode and claude) and add a UI label/i18n entry for it.
- `COPILOT_HOME` env override for the data root (default `~/.copilot`).
- README: document the provider and its token-coverage limits.

## Capabilities

### New Capabilities
- `copilot-provider`: read-only ingestion of Copilot CLI session data — session discovery, subagent tree, token accounting with the no-substitution rule, calls/prompt/trigger endpoints, and provider selection.

### Modified Capabilities
<!-- none: provider-contract (in extract-provider-packages) is used as-is -->

## Impact

- New: `src/provider/copilot/` (+ tests and fixtures).
- Changed: `cmd/dashboard/main.go` (registration), `src/index.html` + `src/i18n.js` (provider label), `README.md`.
- No change to the `provider.Provider` interface or `/api/*` JSON shapes; nodes carry `source: "copilot"`.
- Reads `session-store.db` via the existing pure-Go SQLite driver used by the opencode provider (read-only); no new dependency expected. YAML of `workspace.yaml` is flat `key: value` and can be parsed without a new library.
- Copilot's on-disk format is undocumented and may change between CLI versions; parsing must be tolerant.
