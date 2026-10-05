## Why

For Copilot sessions the dashboard shows empty input, cache, and context-size values. Copilot stores only output tokens per message on disk; the full per-call usage is sent to the IDE live and never saved, and `assistant_usage_events` in `session-store.db` is empty. Context size per call is the main thing the dashboard is meant to measure, so the Copilot provider currently misses its core purpose.

Copilot (CLI and the IDE language server, version 1.0.63 verified) can write OpenTelemetry data to a JSON-lines file when `COPILOT_OTEL_FILE_EXPORTER_PATH` is set. That file has exact per-call token counts for the main agent, including the current context limit, and exact per-subagent totals. Reading it gives the dashboard real numbers.

## What Changes

- The Copilot provider reads the Copilot OpenTelemetry file export (JSON lines) when present, and matches its records to sessions, messages, and subagents already parsed from `events.jsonl`.
- Main-agent calls get input (new tokens), cache read, cache write, output, reasoning, and context size (`ctx`) per call; the session node gets those totals and the largest context (`maxctx`).
- Subagent nodes get exact input, cache read, cache write, output, and reasoning totals from the per-subagent record. Their per-call rows keep output only, and their context size stays empty because Copilot does not record it per call for subagents.
- Input is reported as the new tokens only: the exported prompt size minus cache read minus cache write. This is an explicit, approved exception to the no-substitution rule of the `copilot-provider` capability. Context size is the exported prompt size as reported.
- The OpenTelemetry file becomes the preferred token source; session-store rows, shutdown metrics, and output-only remain fallbacks, without double counting.
- Export files are read from `COPILOT_OTEL_FILE_EXPORTER_PATH` (if set) plus every `<copilot-home>/otel/*.jsonl`, so rotated files keep counting.
- A missing, unreadable, or partly corrupt file never breaks the provider.
- README documents how to enable the export (environment variable for the CLI and for the IDE launcher), what becomes available, and the subagent limitation.

## Capabilities

### New Capabilities
- `copilot-otel-usage`: Reading Copilot's OpenTelemetry file export and using it as the preferred token source for Copilot sessions, subagents, and calls, including per-call context size.

### Modified Capabilities
<!-- `copilot-provider` is not yet archived into openspec/specs, so its token rules are extended by the new capability above instead of a MODIFIED delta. The new capability states which of its rules take precedence. -->

## Impact

- Code: `src/provider/copilot/` (new OpenTelemetry reader; token source selection in usage/tree/calls; tests, fixtures, goldens).
- Docs: README "Copilot CLI provider" section.
- No API shape changes; existing fields (`in`, `cr`, `cw`, `out`, `reason`, `maxctx`, call `ctx`) become filled for Copilot.
- No new dependencies. Read-only access to user files.
- User environment: Copilot must be started with `COPILOT_OTEL_FILE_EXPORTER_PATH` set; sessions recorded before that stay output-only. The export file grows by roughly 8 KB per model call.
