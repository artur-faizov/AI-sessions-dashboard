# Design: extract-provider-packages

## Context

Everything today lives in one flat `package dashboard` (~3.7k lines incl. tests). The
opencode integration is the unprefixed majority (`db.go`, `tree.go`, `calls.go` + the
global `dbx` pool and `cliDBFlag`), claude is `provider_claude*.go`. The shell couples to
provider identity in three places: `server.go:49,149` (`name == "claude"` for
`/api/db`), `run.go:103` (`name == "opencode"` for the startup log), and
`index.html:2055` (`provider !== 'opencode'` for picker visibility). See proposal.md for
motivation; specs/provider-contract and specs/provider-data-paths for the required
behavior.

## Goals / Non-Goals

**Goals:**
- Compile-time isolation: one folder per provider, shell cannot import provider
  internals, providers cannot import the shell
- A third provider becomes: new package with `New()`, one array entry in `main.go`
- Zero change to serialized API shapes except the new `switchable` flag

**Non-Goals:**
- No new providers (copilot is the future consumer, not part of this change)
- No frontend UX changes beyond picker gating on `switchable`
- No persistence layer changes; runtime path switching stays in-memory
- No API changes to `/api/provider` (list + switch semantics already match the spec)

## Decisions

### D1: Package layout — `src/provider/` as a leaf subsystem
```
src/provider/provider.go          package provider: Provider interface + registry
src/provider/types.go             SessionNode, CallRec, TriggerResp + JSON-tagged subtypes
src/provider/opencode/…           package opencode: db, tree, calls, provider adapter
src/provider/claude/…            package claude: provider adapter, index parser
src/dashboard/…                  package dashboard: server, run, browser, assets
```
- Interface + DTOs in a leaf package both sides import; the dashboard shell imports
  nothing from provider subpackages (D3). This kills the import cycle that a
  dashboard-owned interface would create.
- Alternatives: everything under `src/dashboard/provider/` (rejected — providers are an
  app subsystem, not a dashboard detail, and the user chose top-level); keep flat with
  filename prefixes (rejected — no compile-time isolation).

### D2: Provider construction — explicit array of instances in `main.go`
```go
providers := []provider.Provider{opencode.New(cliDBFlag), claude.New()}
dashboard.Run(providers, options…)
```
- First element = default; `--provider` flag indexes into the array; unknown name is
  rejected. No string-keyed constructor registry, no `init()` self-registration — the
  list stays greppable in one place and test binaries control their own provider sets.
- Alternative: `init()`-based self-registration with blank imports (rejected: registration
  by linking side effect, invisible in one place).
- `opencode.New` takes the resolved `--db` flag (empty when unset); env/OS-default
  resolution stays inside the opencode package, so `dbPathCandidates()` in the shell
  disappears — candidates become opencode-internal knowledge exposed through the
  provider.

### D3: Shell↔provider wiring — the shell sees only `provider.Provider`
- The server holds `providers []Provider` + `active Provider` (mutex-guarded, replacing
  `activeProvider`/`providers` map in provider.go).
- Interface surface (exported):
  ```go
  type Provider interface {
      Name() string
      Available() error
      DataPath() string
      FatalWhenMissing() bool
      BuildTree(nowMS int64) ([]*SessionNode, error)
      SessionCalls(sid string) ([]CallRec, error)
      SessionPrompt(sid string) ([]string, error)
      CallTrigger(mid string) (*TriggerResp, error)
      PathSwitchable() bool
      SetDataPath(path string) error
      Candidates() []string
  }
  ```
- `PathSwitchable()/SetDataPath` on the main interface (not a type-asserted optional
  narrower interface): exactly one capability today, and a plugin author shouldn't be
  able to satisfy the interface while accidentally forgetting switchability — explicit
  false is the safe default. Alternative: optional `PathSwitchable` interface asserted by
  the server (rejected: hidden capability, accidental silent no-op).
- DTO fields keep today's JSON tags and order (`source` last on `SessionNode`) —
  byte-identical serialization is a spec requirement; a golden-file test pins it.

### D4: Killing the identity checks
- `server.go` GET `/api/db`: branch on `active.PathSwitchable()` instead of name;
  response gains `"switchable": bool`. Candidates come from a `Candidates() []string`
  method added to the interface — opencode returns its autodetected list, claude
  returns nil. Chosen over an optional narrow interface: the response payload already
  has the field, and a plugin author must not be able to satisfy the contract while
  silently forgetting candidates. This moves `dbPathCandidates`/`resolveDB` knowledge
  out of the shell into the opencode package.
- `server.go` POST `/api/db`: `if !active.PathSwitchable() { 400 }` else
  `active.SetDataPath(path)` → validation error vs success from the provider.
- `run.go` log line: always print `DataPath()` (both providers name their source root).
- `index.html:2055`: gate `dbwrap.hidden` on the `switchable` field from GET `/api/db`.

### D5: `dbx` global → opencode provider state
- The opencode struct owns `{mu sync.RWMutex; path string; pool *sql.DB}` — same
  one-pool-per-generation semantics as today's `dbx`, just instance-scoped.
  `SetDataPath` validates (read-only sqlite schema check = today's `validateDB`),
  swaps under the write lock; readers take the read lock per request. No shell code
  touches pools anymore.
- `cliDBFlag` disappears from the shell; `resolveDB`/`validateDB` move into
  `provider/opencode`.

### D6: Test seams
- Shell tests construct fake providers (small struct in `src/dashboard/*_test.go`)
  implementing the interface — no sqlite fixtures for switching/picker tests.
- `TestDefaultProviderIsOpencode` becomes "default = first array entry" in the shell
  tests; the literal array lives in `main.go`, and the shell test asserts ordering
  semantics with its own two fake providers (main's array is one line, compile-checked).
- Shared tiny helpers (`jdump`, `httpGet/httpPost`, `apiServer`) are duplicated per
  package where needed; extract a helper package only if a second duplication appears.
- Non-test helpers used by both providers (`clip`, `expandUser`, `activeWindowMS`) move
  to the leaf `provider` package as exported utilities, so provider packages never import
  each other.

## Risks / Trade-offs

- [Exported DTOs are now API surface] → keep tags/order pinned by a golden JSON test
  moved into `src/provider` tests; any field change must update it deliberately.
- [Race window when swapping pools] → same RWMutex generation pattern as today; add a
  concurrent switch+read test in `provider/opencode` (go test -race).
- [Interface has 11 methods — plugin boilerplate] → acceptable at 3 providers; if it
  grows painful, introduce an embedded `Base` struct providing no-op defaults later.
- [Behavior drift during move] → tests move unchanged first, code follows; `go vet` +
  `go test -race ./...` green before/after each task.

## Migration Plan

Pure refactor, single repo, no deployment. Order: (1) create `src/provider` contract +
DTOs and flip dashboard to them, (2) move opencode files behind `New()`, (3) move claude
files, (4) rewire main.go array + shell capability checks, (5) frontend one-liner, (6)
test split. Rollback = git revert; no data or config migration.

## Open Questions

- None blocking. (Whether `Candidates() []string` deserves a home on the interface vs
  opencode-only helper was settled in D4: main interface, nil for non-switchable
  providers.)
