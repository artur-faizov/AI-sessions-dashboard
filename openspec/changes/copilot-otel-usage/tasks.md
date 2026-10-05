## 1. Event parsing

- [x] 1.1 Record `apiCallId` on assistant items in `events.go` and index it per session (`apiIDs` → agent + item index); verify with an `events_test.go` case that the root and subagent messages of the `sub-0002` fixture resolve to the right agent

## 2. Export reader

- [x] 2.1 Add `otel.go` with path discovery (`COPILOT_OTEL_FILE_EXPORTER_PATH` plus `<root>/otel/*.jsonl`, de-duplicated) and wire it into `New()`; verify with a test using `t.Setenv` and a temp copilot home that both sources are found and a shared path is read once
- [x] 2.2 Implement the line parser: only spans with `gen_ai.operation.name` = `chat` (named `chat` or `chat <model>`) and conversation and response ids, numeric-or-string attributes, missing attributes = 0, skipping invalid JSON, metrics, and other spans; verify with table tests over sample lines taken from a real export (numbers, strings, garbage, metric line)
- [x] 2.3 Implement incremental per-file reading (offset to the last newline, partial-line carry-over, re-read on inode change or shrink) and the merged index with spanId de-duplication and per-key summing; verify with tests that append to a file between reads, write a half line then complete it, truncate the file, and put the same span in two files

## 3. Token precedence

- [x] 3.1 Add the `srcOTel` source to `nodeTokens` (root: per-item from matched spans plus output of unmatched items, `maxctx` = max input_tokens; subagent: sums of its spans, `maxctx` 0; fresh input = max(0, input − cr − cw)) and pass a combined `tokenData` through `tree`/`calls`; verify with `usage_test.go` cases for each spec scenario under "Node and call token precedence with export data"
- [x] 3.2 Make root calls use matched spans (all fields, `ctx` = input_tokens) while subagent calls stay output-only; verify with `calls_test.go` cases, including the "call without matched span" scenario
- [x] 3.3 Ensure no double counting with shutdown metrics or session-store rows and correct subtree sums; verify with a `tree_test.go` case on a fixture that has spans, a shutdown event, and a subagent

## 4. Fixtures and goldens

- [x] 4.1 Add a fixture session `otel-0006` (root calls + one subagent + an unmatched span + a duplicate span) and `testdata/home/otel/export.jsonl`, regenerate goldens with `go test ./src/provider/copilot/ -update`, and review the golden diff so that only the new session changes

## 5. Docs and verification

- [x] 5.1 Update the README "Copilot CLI provider" section: how to enable the export (shell, `environment.d`, IDE `.desktop` launcher), which fields get filled, the subagent limitation, file growth and rotation into `~/.copilot/otel/`; verify by reading it against the spec
- [x] 5.2 Run `go vet ./...`, `go test ./...`, and `make test`, and verify that all pass
- [x] 5.3 After GoLand has been restarted with the variable, verify against real data: `copilot-language-server` has `COPILOT_OTEL_FILE_EXPORTER_PATH` in `/proc/<pid>/environ`, `~/.copilot/otel/copilot-otel.jsonl` grows, and the dashboard (`--provider copilot`) shows non-zero `in`/`maxctx` for the current GoLand session and per-call `ctx`, matching the span values
