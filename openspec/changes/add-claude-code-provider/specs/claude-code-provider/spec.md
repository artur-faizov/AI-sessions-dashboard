# Spec Delta: claude-code-provider

## Purpose

Read-only ingestion of Claude Code JSONL transcripts so the dashboard can display Claude Code sessions, their token usage, and their subagent tree through the same API as opencode sessions.

## ADDED Requirements

### Requirement: Claude Code session discovery
The system SHALL discover Claude Code sessions by scanning `~/.claude/projects/<dir-slug>/<session-id>.jsonl` and SHALL derive each session's working directory from the enclosing slug or per-line `cwd`.

#### Scenario: Sessions listed
- **WHEN** the tree API is requested with the claude provider active
- **THEN** every readable `<session-id>.jsonl` appears as a session node with a directory, title, and timestamps

#### Scenario: Malformed lines tolerated
- **WHEN** a transcript contains lines that are not valid JSON
- **THEN** the system skips those lines and still serves the rest of the session

### Requirement: Subagent tree reconstruction
The system SHALL represent each `<session-id>/subagents/agent-*.jsonl` as a child session node and SHALL attach it to its parent session using the `toolUseId` from the matching `meta.json`, falling back to `spawnDepth` and timestamps when the tool call is not found.

#### Scenario: Subagent as child node
- **WHEN** a session has subagent files under its `subagents/` directory
- **THEN** each subagent appears as a child of that session in the tree, with an agent name from `meta.json` `agentType`

#### Scenario: Legacy inline sidechain lines
- **WHEN** a transcript uses the older format with subagent content inline (`isSidechain` lines) and no `subagents/` directory
- **THEN** the session is still shown flat (no subagent children) and no error occurs

### Requirement: Token accounting
The system SHALL map Claude Code `usage` fields to the dashboard token fields: `input_tokens` → input, `output_tokens` → output, `cache_read_input_tokens` → cache read, `cache_creation_input_tokens` → cache write; reasoning tokens SHALL be reported as 0. Per-session totals SHALL be computed by summing per-assistant-message usage. Duplicate assistant lines (retries) SHALL be counted once by deduplicating on message id. Assistant messages with zero usage or synthetic model markers SHALL be excluded.

#### Scenario: Totals match transcript
- **WHEN** a session's deduplicated assistant messages have usages U1..Un
- **THEN** the session node's token totals equal the sums of U1..Un per token field

#### Scenario: Retried response not double-counted
- **WHEN** an assistant response appears multiple times with the same message id
- **THEN** its usage is counted exactly once

### Requirement: Provider selection and API compatibility
The system SHALL allow selecting the provider via CLI flag or environment variable, defaulting to the opencode provider with unchanged behavior. All `/api/*` endpoints SHALL return the same JSON shapes for both providers, and each session node SHALL carry a `source` field identifying its provider.

#### Scenario: Default provider unchanged
- **WHEN** the dashboard starts with no provider flag
- **THEN** it reads opencode's SQLite DB exactly as before and nodes have `source: "opencode"`

#### Scenario: Claude provider selected
- **WHEN** the dashboard starts with the claude provider selected
- **THEN** the tree, prompt, calls, and trigger endpoints return data derived from Claude Code transcripts with `source: "claude"`

#### Scenario: Missing data source
- **WHEN** the selected provider's data files do not exist
- **THEN** the dashboard prints a clear error naming the missing path and exits without serving
