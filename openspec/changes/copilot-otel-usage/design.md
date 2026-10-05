## Context

The `copilot` provider (`src/provider/copilot/`) parses `events.jsonl` per session into streams (root `""` and one per subagent `agentId`) and picks tokens per node in `usage.go` (`nodeTokens`: session-store rows → shutdown metrics → output-only). Calls come from `calls.go`. Sessions are cached by file mtime/size.

Facts verified with Copilot 1.0.63 (CLI run with `COPILOT_OTEL_FILE_EXPORTER_PATH`), see proposal.md for motivation:
- The exporter appends one JSON object per line: `{"type":"span", traceId, spanId, parentSpanId, name, attributes, events, ...}` and `{"type":"metric", ...}`. Several processes append to the same file. Lines can be 8 KB+ (the `gen_ai.tool.definitions` attribute) and much larger with content capture.
- Attribute numbers may be JSON numbers or strings.
- Model-call spans have `gen_ai.operation.name: "chat"`; the span name is `chat <model>` from the CLI and plain `chat` from the GoLand language server (verified after the restart, same attributes, conversation id = session id).
- Main agent: one `chat` span per API call. `gen_ai.response.id` equals `assistant.message.data.apiCallId` in `events.jsonl`; `gen_ai.conversation.id` equals the session id. `input_tokens` = new + cache read + cache write.
- Subagent: one `chat` span per subagent run carrying the sums of all its calls; its `gen_ai.response.id` is the `apiCallId` of the subagent's last call. It sits under `invoke_agent <name>` → `execute_tool task` (whose `gen_ai.tool.call.id` is the subagent `agentId`). Nested subagents get their own span; a parent's span does not include its children.
- The main agent's spans do not include subagent usage.

## Goals / Non-Goals

**Goals:**
- Fill `in`, `cr`, `cw`, `out`, `reason`, `maxctx`, and call `ctx` for Copilot from the export, per the spec.
- Read large, growing export files cheaply on every tree/calls request.

**Non-Goals:**
- Writing, rotating, or configuring the export file (the user's environment does that; README explains it).
- Per-call numbers or context size for subagents (not exported).
- Showing the context limit (`github.copilot.token_limit`) in the UI.
- OTLP network ingestion.

## Decisions

### 1. Match by response id, not by span parent chain
A `chat` span is joined to `events.jsonl` via `(conversation.id, response.id) → apiCallId → (agentId, item)`. The owning agent decides per-call (root) vs aggregate (subagent).
- Why: one lookup handles root calls, subagents, and nested subagents, and does not need the span tree (whose parent spans may sit in a different, later line or be missing).
- Alternative: walk `chat → invoke_agent → execute_tool task` to get the agentId. Rejected as the main path: needs all spans of a trace in memory and fails on truncated traces. Not needed since the response id already identifies the agent.
- `events.go` stores `apiCallId` on assistant items and keeps an `apiIDs map[string]midRef` per session.

### 2. Incremental per-file reader with dedup
New `otel.go` holds `otelReader{paths, files map[path]*otelFile}`, guarded by a mutex. Per file: device+inode, read offset, and parsed spans. On each request:
- Stat every candidate path (env path, plus a glob of `<root>/otel/*.jsonl`, de-duplicated by cleaned absolute path).
- If the inode changed or the size dropped below the offset, drop that file's spans and re-read from 0.
- Otherwise read from the offset to the last complete `\n` with `bufio.Reader.ReadSlice`/`ReadBytes` (no `bufio.Scanner` token limit) and leave a partial last line for the next pass.
- Cheap pre-filter: skip lines without `"type":"span"` or without `gen_ai.response.id` before `json.Unmarshal` into a minimal struct (`spanId`, `name`, `attributes map[string]json.RawMessage`).
- Keep only spans with `gen_ai.operation.name == "chat"` and conversation and response ids. Each stores spanId, convID, respID, input, cr, cw, out, reason.
- The merged index `map[otelKey{conv, resp}]otelTokens` is rebuilt only when some file changed. Spans are de-duplicated by spanId across files, and distinct spans with the same key are summed.
- Alternative: re-read whole files each request. Rejected because the file grows without bound (~8 KB per call).

### 3. Token source precedence
`nodeTokens` gets a new top source `srcOTel`, used when the node has ≥1 matched span:
- Root: iterate root assistant items in order. A matched item contributes all fields and `ctx = input_tokens`, and `maxctx` is the max of those. An unmatched item contributes `out` only.
- Subagent: sum the matched spans whose response id belongs to that subagent's items; `maxctx = 0`.
- `calls()` uses the same per-item rule for root calls; subagent calls stay output-only.
- Fresh input = `max(0, input − cr − cw)`, approved by the user as the only cross-field arithmetic.
- Root shutdown subtraction in `tree.go` stays tied to `srcShutdown`, so it never runs for an OTel root.
- Alternative: mix sources within a node, e.g. shutdown totals for the remainder. Rejected because it double counts and breaks the no-substitution intent.

### 4. Plumbing
Introduce `tokenData{usage usageIndex; otel otelIndex}`, loaded once per request (`p.loadTokens()`), and pass it where `usageIndex` is passed today (`tree`, `nodeTokens`, `calls`). The OTel index is shared and read-only once built.

### 5. Paths
`New()` captures `COPILOT_OTEL_FILE_EXPORTER_PATH` (expanded with `provider.ExpandUser`) and `<root>/otel`. The glob is re-evaluated on each request, so new or rotated files are picked up.

## Risks / Trade-offs

- [Copilot changes span names or attributes in a future version] → Unknown shapes are skipped. The provider falls back to the old sources, and the README notes the verified version.
- [The IDE language server may emit different conversation ids than the CLI] → A post-restart verification task checks the GoLand session ids against the export before the change is closed.
- [Memory growth: ~200 bytes per call kept in memory] → Acceptable (1M calls ≈ 200 MB worst case); only needed attributes are stored.
- [Subagent span emitted only when the subagent finishes] → A running subagent shows output-only until then. This is inherent to the export.
- [Concurrent writer leaves a half line] → Read only up to the last newline.
- [A file that is rotated by copy-truncate] → The size drop is detected and the file re-read. Spans copied to an `otel/*.jsonl` archive are de-duplicated by spanId.

## Migration Plan

No data migration. The user enables the export (already done globally: `~/.config/environment.d`, `~/.profile`, and the GoLand `.desktop` launcher). Without it, behavior is unchanged. Rollback is reverting the code.
