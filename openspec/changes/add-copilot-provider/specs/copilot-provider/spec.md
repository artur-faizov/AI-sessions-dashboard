## Purpose

Read-only ingestion of GitHub Copilot CLI session data so the dashboard can display Copilot sessions, their subagent tree, and their token usage through the same API as the opencode and Claude Code providers.

## ADDED Requirements

### Requirement: Copilot session discovery
The system SHALL discover Copilot CLI sessions by scanning `<copilot-home>/session-state/<session-id>/events.jsonl`, where `<copilot-home>` is `COPILOT_HOME` if set, else `~/.copilot`. Each session's working directory, title, and timestamps SHALL come from `workspace.yaml` when present, falling back to the `session.start` event and first/last event timestamps.

#### Scenario: Sessions listed
- **WHEN** the tree API is requested with the copilot provider active
- **THEN** every session directory with a readable `events.jsonl` appears as a root node with a directory, title, and created/updated timestamps, and `source: "copilot"`

#### Scenario: Malformed lines tolerated
- **WHEN** `events.jsonl` contains lines that are not valid JSON or have unknown event types
- **THEN** the system skips those lines and still serves the rest of the session

#### Scenario: Active session
- **WHEN** a session directory holds an `inuse.*.lock` file or its last event is recent
- **THEN** the session node is marked active

### Requirement: Subagent tree reconstruction
The system SHALL represent each subagent started in a session (a `subagent.started` event) as a child node of that session, identified by its tool call id. Events carrying that `agentId` SHALL be attributed to the child, not to the parent. The child's agent name SHALL come from `agentName` and its model from the event's `model`.

#### Scenario: Subagent as child node
- **WHEN** a session contains `subagent.started` events
- **THEN** each subagent appears as a child of that session, with its agent name, model, start time, and (when `subagent.completed` exists) duration

#### Scenario: Nested subagent
- **WHEN** a subagent's own events start another subagent
- **THEN** the nested subagent appears as a child of the subagent that started it

#### Scenario: Unfinished subagent
- **WHEN** a subagent has `subagent.started` but no `subagent.completed`
- **THEN** it is still shown as a child, without error

### Requirement: Token accounting without substitution
The system SHALL map each dashboard token field (input, output, cache read, cache write, reasoning) only from a Copilot source that reports that exact field. It MUST NOT derive a field from other fields or put one field's value into another (for example: total tokens MUST NOT be reported as input, and input MUST NOT be computed as total minus output). A field that no available source reports SHALL be reported as 0.

Sources, in order of preference for each node:
1. Per-call usage rows in `<copilot-home>/session-store.db` (`assistant_usage_events`) for that session/agent: all five fields, per call.
2. Otherwise, for root sessions, per-model usage in the session's `session.shutdown` events: input, output, cache read, cache write, reasoning, as session totals.
3. Otherwise, per-message `outputTokens` on `assistant.message` events: output only.

#### Scenario: Per-call rows available
- **WHEN** `assistant_usage_events` has rows for a session
- **THEN** the node's input, output, cache read, cache write, and reasoning totals equal the sums of those rows, and each row is one entry in the calls API

#### Scenario: Only shutdown metrics available
- **WHEN** there are no usage rows but the session has `session.shutdown` events with model metrics
- **THEN** the root node's totals come from those metrics, and subagent nodes report only output from their `outputTokens`

#### Scenario: Only output tokens available
- **WHEN** neither usage rows nor shutdown metrics exist
- **THEN** the node reports output as the sum of `outputTokens` and input, cache read, cache write, and reasoning as 0

#### Scenario: Subagent total not re-labelled
- **WHEN** a `subagent.completed` event has `totalTokens` and no per-field data exists for that subagent
- **THEN** that number does not appear in any of the node's token fields

#### Scenario: No double counting
- **WHEN** a session has both usage rows and shutdown metrics
- **THEN** only the usage rows are counted for that session

### Requirement: Calls, prompt, and trigger endpoints
The system SHALL serve `/api/calls`, `/api/prompt`, and `/api/trigger` for Copilot sessions in the same JSON shapes as the other providers. Calls SHALL be one entry per assistant API call, with token fields set by the no-substitution rule. The prompt of a root session SHALL be its user messages; the prompt of a subagent SHALL be the prompt argument of the tool call that started it. The trigger of a call SHALL be the user message or tool results that preceded it.

#### Scenario: Calls listed
- **WHEN** the calls API is requested for a Copilot session or subagent
- **THEN** it returns that node's assistant calls in time order, each with its message id and the token fields available for it

#### Scenario: Subagent prompt
- **WHEN** the prompt API is requested for a subagent node
- **THEN** it returns the prompt text passed to the tool call that started that subagent

#### Scenario: Unknown ids
- **WHEN** the calls or prompt API is requested for an unknown session id, or the trigger API for an unknown message id
- **THEN** the response is the same error or empty result the other providers give

### Requirement: Copilot provider selection
The system SHALL register the copilot provider under the name `copilot`, selectable through `--provider copilot`, `PROVIDER=copilot`, and the UI provider switcher, without changing the default provider. If the copilot data root does not exist when it is selected at startup, the system SHALL print an error naming the missing path.

#### Scenario: Copilot selected
- **WHEN** the dashboard starts with `--provider copilot`
- **THEN** the tree, calls, prompt, and trigger endpoints return Copilot data and nodes have `source: "copilot"`

#### Scenario: Default unchanged
- **WHEN** the dashboard starts with no provider flag
- **THEN** the default provider is the same as before this change

#### Scenario: Missing data root
- **WHEN** the copilot provider is selected and `<copilot-home>/session-state` does not exist
- **THEN** the dashboard prints an error naming that path

#### Scenario: Store database absent
- **WHEN** `session-store.db` is missing, locked, or has no `assistant_usage_events` table
- **THEN** the provider still serves sessions using the other token sources
