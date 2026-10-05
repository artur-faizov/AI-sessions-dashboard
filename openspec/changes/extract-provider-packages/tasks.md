## 0. Baseline

- [ ] 0.1 Record baseline: `go vet ./...` and `go test -race ./...` green on the current tree; capture a golden `/api/tree` + `/api/calls/<sid>` JSON dump from the existing opencode test fixture for later byte-comparison

## 1. Provider contract package (`src/provider`)

- [ ] 1.1 Create `src/provider/provider.go` with the exported `Provider` interface (Name, Available, DataPath, FatalWhenMissing, BuildTree, SessionCalls, SessionPrompt, CallTrigger, PathSwitchable, SetDataPath, Candidates); verify `go build ./src/provider` succeeds and the package imports nothing from `src/dashboard`
- [ ] 1.2 Move DTOs to `src/provider/types.go` as exported `SessionNode`, `CallRec`, `TriggerResp`, `TriggerPartText`, `TriggerPartTool` with unchanged JSON tags and field order (`source` last on `SessionNode`); verify with a golden JSON test in `src/provider` that serializes a populated node/call/trigger and matches the 0.1 bytes
- [ ] 1.3 Move shared helpers `clip`, `expandUser`, `activeWindowMS` into `src/provider` as exported utilities; verify their existing unit tests pass in the new package
- [ ] 1.4 Switch `src/dashboard` to the exported DTOs/helpers (temporary adapters allowed); verify `go test -race ./src/...` stays green

## 2. Opencode provider package (`src/provider/opencode`)

- [ ] 2.1 Move `db.go`, `tree.go`, `calls.go`, `provider_opencode.go` into `package opencode`; replace the `dbx` global and `cliDBFlag` with fields on an opencode struct (`mu sync.RWMutex`, `path`, `pool`, `cliPath`); verify `go build ./src/provider/opencode` succeeds and `grep -rn "dbx\|cliDBFlag" src/dashboard` returns nothing
- [ ] 2.2 Add `opencode.New(cliDBPath string) *Provider` that resolves CLI → `DB_PATH` env → OS default internally (former `resolveDB`/`dbCandidates`); export `ParseDBFlag(args)` for `main`; verify resolution-order tests pass in the opencode package
- [ ] 2.3 Implement `PathSwitchable() == true`, `SetDataPath` (validates via former `validateDB`, swaps pool under write lock, keeps previous path on failure) and `Candidates()`; verify invalid-path test leaves previous path active
- [ ] 2.4 Move `db_test.go`, `tree_test.go`, `calls_test.go` and opencode parts of `provider_test.go` into `src/provider/opencode`; add a concurrent `SetDataPath` + `BuildTree`/`SessionCalls` test; verify `go test -race ./src/provider/opencode` passes

## 3. Claude provider package (`src/provider/claude`)

- [ ] 3.1 Move `provider_claude.go` and `provider_claude_index.go` into `package claude` with exported `claude.New()`; implement `PathSwitchable() == false`, `SetDataPath` returning an "unsupported" error, `Candidates() == nil`; verify `go build ./src/provider/claude` succeeds and it imports only `src/provider`
- [ ] 3.2 Move `provider_claude_test.go` into `src/provider/claude`; verify `go test -race ./src/provider/claude` passes with unchanged assertions

## 4. Shell rewiring (`src/dashboard`, `cmd/dashboard`)

- [ ] 4.1 Change `dashboard.Run` to accept `[]provider.Provider` (plus args); server holds `providers` slice + mutex-guarded `active`; delete the `providers` map, `activeProvider` global and `selectProvider`; first element is the default, `PROVIDER` env / `--provider` flag selects by `Name()`, unknown name exits 1 listing names from the slice; verify shell tests with two fake providers cover default=first, explicit override, unknown rejected
- [ ] 4.2 Update `cmd/dashboard/main.go` to build `[]provider.Provider{opencode.New(opencode.ParseDBFlag(args)), claude.New()}` and pass it to `Run`; verify `go build ./cmd/dashboard` and `--version` still work
- [ ] 4.3 `GET /api/db`: drop the `name == "claude"` branch; respond with `path`, `found`, `error`, `candidates` (empty list when nil) and new `switchable` from the active provider; verify fake-provider tests for both switchable and non-switchable responses
- [ ] 4.4 `POST /api/db`: replace the name check with `!active.PathSwitchable()` → 400 "path switching is not supported for the active provider"; otherwise call `SetDataPath` and map its error to 400 naming the path; verify shell tests for reject, success and invalid-path cases
- [ ] 4.5 `POST /api/provider` and `GET /api/provider`: list/select from the provider slice (sorted names unchanged in response); verify existing provider-switch tests pass against fake providers
- [ ] 4.6 `run.go`: replace the `name == "opencode"` log branch and the "Claude data not found" message with provider-neutral output based on `DataPath()`/`Available()`; verify `run_test.go` assertions updated and passing
- [ ] 4.7 Verify isolation: `grep -rnE '"(opencode|claude)"' src/dashboard/*.go` (non-test) returns nothing, and `go list -deps ./src/dashboard` does not include `src/provider/opencode` or `src/provider/claude`

## 5. Frontend

- [ ] 5.1 In `src/index.html`, gate `dbwrap.hidden` on `!info.switchable` from `GET /api/db` instead of `info.provider !== 'opencode'`; verify manually with `make dev`: picker visible for opencode (also when DB is missing), hidden for claude

## 6. Final verification

- [ ] 6.1 Run `go vet ./...` and `go test -race ./...`; all packages green with tests colocated per package (no sqlite fixtures in `src/dashboard` tests)
- [ ] 6.2 Compare live `/api/tree` and `/api/calls/<sid>` output against the 0.1 golden dump for the same fixture; verify byte-identical (the only API shape change is `switchable` on `/api/db`)
- [ ] 6.3 Run `make build-all`; verify all platform binaries build and the host binary starts against the real opencode DB and with `--provider claude`
- [ ] 6.4 Update README provider/architecture notes to describe `src/provider/<name>` layout and how to add a provider (new package + one entry in `main.go`)
