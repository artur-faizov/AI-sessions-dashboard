# Proposal: extract-provider-packages

## Why

Opencode and Claude Code integrations live as unprefixed and prefixed files in one flat
`package dashboard`, coupled through globals (`dbx`, `activeProvider`) and identity checks
(`name == "claude"`, `name == "opencode"`) scattered across the shell. Adding a third data
source (e.g. GitHub Copilot) means copying that pattern and touching shared code in more
places. Extracting providers into isolated packages behind one contract makes a new
integration a self-contained folder plus one array entry in `main.go`.

## What Changes

- New `src/provider/` package tree (top-level, alongside `src/dashboard/`):
  - `provider` — the `Provider` contract and shared DTOs (`SessionNode`, `CallRec`,
    `TriggerResp`), no dependencies on the shell
  - `provider/opencode` — sqlite DB access (`db.go`, `tree.go`, `calls.go`), provider
    state moves from package globals (`dbx`) to struct fields
  - `provider/claude` — projects-dir provider and JSONL index parsing
- `cmd/dashboard/main.go` constructs every provider via `New()` and passes an array of
  instantiated providers to the shell; array order is priority, the first element is the
  default (no auto-fallback to available providers)
- Provider interface gains path-switching capabilities: `PathSwitchable() bool` and
  `SetDataPath(path) error`
- Identity checks in the shell replaced with capability checks:
  - `GET /api/db` returns a `switchable` flag (false for non-switchable providers) instead
    of branching on provider name
  - `POST /api/db` rejects non-switchable providers with 400 (same client-visible error
    class as today's claude rejection, now generalized)
  - startup log line uses `DataPath()` uniformly
- Frontend DB-picker visibility gates on the `switchable` flag instead of the provider
  name (only change to `index.html`; no new UX)
- Tests colocated per package: provider internals tested in their own packages; shell
  tests never construct sqlite fixtures for switching behavior
- **BREAKING** (repo-internal): `sessionNode`/`callRec`/`triggerResp` become exported types
  in `provider`; `src/dashboard` consumers of the unexported names must be updated. No
  external API, CLI flag, or endpoint shape breaks.

## Capabilities

### New Capabilities

- `provider-contract`: the provider interface, DTO ownership, per-provider subpackage
  isolation, registration as an array of instances in `main`, default-selection order,
  and startup behavior when the default provider's data source is missing
- `provider-data-paths`: capability-based data-path switching — the `switchable` flag on
  `GET /api/db`, `POST /api/db` switching rules and validation, and frontend picker
  visibility gating

### Modified Capabilities

(none — `openspec/specs/` has no archived capabilities yet)

## Impact

- **Code**: all of `src/dashboard/` (files move: `provider*.go`, `db.go`, `tree.go`,
  `calls.go` → `src/provider/opencode/`; `provider_claude*.go` → `src/provider/claude/`),
  `cmd/dashboard/main.go` (constructs and passes the provider array), `src/index.html`
  (one line: picker visibility), `src/dashboard/*_test.go` (split per package)
- **APIs**: `GET /api/db` gains a `switchable` boolean; everything else unchanged in shape
- **Dependencies**: none new; same Go stdlib surface (`database/sql`, `embed`)
- **Risks**: exported DTO field JSON tags must serialize byte-identically to today
  (`source` stays the last JSON field on session nodes); runtime path switching must
  remain race-free after `dbx` becomes provider-owned state
