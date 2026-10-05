## Purpose

Use the GitHub Copilot OpenTelemetry file export as the preferred source of token usage for Copilot sessions, so the dashboard can show real input, cache, and per-call context size instead of output tokens only.

## ADDED Requirements

### Requirement: OpenTelemetry export discovery
The system SHALL read Copilot OpenTelemetry JSON-lines export files from the path in `COPILOT_OTEL_FILE_EXPORTER_PATH` when that variable is set, and also from every `*.jsonl` file in `<copilot-home>/otel/` (so rotated or archived exports placed there keep counting). Files SHALL be opened read-only. A missing directory or file SHALL be treated as no export data. Lines that are not valid JSON, are not spans, or lack the needed attributes SHALL be skipped without failing the provider.

#### Scenario: Export path from environment
- **WHEN** `COPILOT_OTEL_FILE_EXPORTER_PATH` points to an existing export file
- **THEN** token data in that file is used for matching sessions

#### Scenario: Default export directory
- **WHEN** `<copilot-home>/otel/` contains `.jsonl` export files, whether or not the variable is set
- **THEN** token data from all of those files is used, and a file reached both ways is read once

#### Scenario: No export present
- **WHEN** no export file exists
- **THEN** the provider serves sessions with the token sources it used before, without error

#### Scenario: Corrupt or partial lines
- **WHEN** an export file contains invalid JSON lines, metric records, or a truncated last line
- **THEN** those lines are ignored and valid spans in the same file are still used

#### Scenario: File grows while the dashboard runs
- **WHEN** new spans are appended to an export file after it was first read
- **THEN** the next tree or calls request reflects the new spans

### Requirement: Matching export records to sessions
The system SHALL match each exported model-call span (a span whose `gen_ai.operation.name` is `chat`; its name is `chat` or `chat <model>` depending on the Copilot client) to a Copilot session by its `gen_ai.conversation.id`, and to an assistant message of that session by its `gen_ai.response.id` equal to the message's `apiCallId`. The agent that produced the matched message (the main agent or a subagent) SHALL own the span. Spans that match no session or no message SHALL be ignored. A span that appears more than once (same span id) SHALL be counted once.

#### Scenario: Main-agent call matched
- **WHEN** a model-call span's response id equals the `apiCallId` of a main-agent assistant message in the same session
- **THEN** that span's tokens are attributed to that call of the session node

#### Scenario: Subagent record matched
- **WHEN** a model-call span's response id equals the `apiCallId` of an assistant message emitted by a subagent
- **THEN** that span's tokens are attributed to that subagent node as a whole, not to an individual call

#### Scenario: Span name without model
- **WHEN** a model-call span is named `chat` (as written by the IDE language server) instead of `chat <model>`
- **THEN** it is matched the same way

#### Scenario: Unmatched span
- **WHEN** a span's conversation id or response id is unknown
- **THEN** it does not change any node or call

#### Scenario: Duplicate span lines
- **WHEN** the same span id appears in two lines or two files
- **THEN** its tokens are counted once

### Requirement: Token values from the export
For a matched span the system SHALL report: cache read = `gen_ai.usage.cache_read_input_tokens`; cache write = `gen_ai.usage.cache_creation_input_tokens`; output = `gen_ai.usage.output_tokens`; reasoning = `gen_ai.usage.reasoning_output_tokens`; input = `gen_ai.usage.input_tokens` minus cache read minus cache write, floored at 0. Missing attributes SHALL count as 0. For main-agent calls, the call context size SHALL be `gen_ai.usage.input_tokens` as reported. This input subtraction is the only arithmetic allowed between fields; no other field SHALL be derived from another.

#### Scenario: Input excludes cache
- **WHEN** a main-agent span reports input 24000, cache read 23545, cache write 448, output 86
- **THEN** the call shows input 7, cache read 23545, cache write 448, output 86, and context size 24000

#### Scenario: Missing cache attributes
- **WHEN** a span reports input 23523 and no cache attributes
- **THEN** the call shows input 23523, cache read 0, cache write 0, and context size 23523

### Requirement: Node and call token precedence with export data
A node that has at least one matched span SHALL use export data in preference to the token sources of the `copilot-provider` capability, and SHALL NOT add session-store rows or shutdown metrics for that node.
- Calls of a session (main agent) node: a call with a matched span gets all token fields and context size from it; a call without one keeps output from `outputTokens`, other token fields 0, and context size 0.
- Session node: totals are the sums over its calls as defined above; `maxctx` is the largest call context size.
- Subagent node: totals are the sums over its matched spans; `maxctx` stays 0; its calls keep output from `outputTokens` only, with context size 0.
Nodes with no matched span SHALL keep the token sources and rules of the `copilot-provider` capability. Subtree totals SHALL equal the node's own totals plus its descendants' subtree totals, without counting any span twice.

#### Scenario: Session with export data
- **WHEN** a session has two main-agent calls with matched spans of context sizes 24213 and 24616
- **THEN** the session node's `maxctx` is 24616, its totals are the sums of both spans, and both calls show their own context size

#### Scenario: Subagent with export data
- **WHEN** a subagent has one matched span with input 55308, cache read 40000, cache write 15000, output 574
- **THEN** the subagent node shows input 308, cache read 40000, cache write 15000, output 574, `maxctx` 0, and its calls show output only

#### Scenario: Export not double counted with shutdown metrics
- **WHEN** a session has matched spans and also `session.shutdown` metrics
- **THEN** the session node's totals come from its calls as defined above, not from the shutdown metrics

#### Scenario: Older session without export data
- **WHEN** a session was recorded before the export was enabled
- **THEN** its node and calls show the same values as without this capability

#### Scenario: Call without matched span in a session that has export data
- **WHEN** a main-agent call has no matched span but other calls in the session do
- **THEN** that call shows output from `outputTokens`, other token fields 0, and context size 0, and the session totals include that output
