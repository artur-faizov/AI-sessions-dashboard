# Design: port-backend-to-go

## Context

The backend today is a 522-line Python file (`dashboard.py`) using
`http.server`, stdlib `sqlite3` with per-thread connections, and
PyInstaller for distribution. The frontend (`src/index.html`,
`src/i18n.js`) is served raw and is out of scope. The API surface and
JSON shapes are the de-facto contract consumed by the frontend and
covered by `test/test_dashboard.py` (30 tests, which will be ported)
and indirectly by the Node frontend tests. See proposal.md for
motivation.

## Goals / Non-Goals

**Goals:**

- Behavior parity with the current backend: identical API routes,
  JSON field names/shapes, status codes, and startup conventions
  (`PORT`, `NO_BROWSER`, `--db`, `--db=`, `--version`).
- Single pure-Go binary per platform; all targets cross-compilable
  from one machine.
- Port the Python test suite to Go so `make test` covers backend and
  frontend as before.

**Non-Goals:**

- No frontend changes (no new features, no UI redesign).
- No schema changes to the opencode database; read-only access only.
- No config files or flags beyond the existing ones.

## Decisions

### D1: Go module layout

```
cmd/dashboard/main.go  — thin entrypoint: os.Exit(dashboard.Run(os.Args[1:]))
src/embed.go           — package webui: go:embed of src/index.html, src/i18n.js
src/dashboard/run.go   — flag parsing, port fallback, browser open, shutdown
src/dashboard/server.go — routes (net/http ServeMux), handlers, JSON helpers
src/dashboard/db.go    — candidates, validation, read-only pool, switching
src/dashboard/tree.go  — build_tree, session_max_contexts, beats, subtree math
src/dashboard/calls.go — session_calls, call_trigger, human-message detection
src/dashboard/browser.go — opens the dashboard URL in the system browser
src/dashboard/assets.go — {{VERSION}} swap; dev-mode disk reads of src/
```

A thin `cmd` entrypoint plus a `package dashboard` library and a `webui`
embed package for the frontend assets. Alternative considered:
multi-package layout (`internal/api`, `internal/store`) — rejected for
this size; the file split above keeps units readable without ceremony.

### D2: SQLite driver — modernc.org/sqlite (pure Go)

Cross-compilation is the entire point, so CGO is off (`CGO_ENABLED=0`).
`modernc.org/sqlite` is the mature pure-Go driver; `mattn/go-sqlite3`
would require a C toolchain per target and kills cross-compilation.

Read-only enforcement uses the URI form
`file:<quoted-path>?mode=ro&immutable=0` — matching Python's
`mode=ro` query parameter, including URI-escaping of the path
(`url.PathEscape` semantics per RFC 3986, like `urllib.parse.quote`).

Alternatives considered: porting to a different embedded DB or
shipping a wasm runtime — pointless, the data lives in sqlite.

### D3: Connection management

Mirror the Python model: one `*sql.DB` per generation. On switch
(`POST /api/db`) the generation counter increments under a mutex and
the old pool is closed lazily; requests grab the current pool via
atomic load of a small struct (`pool + gen`). No per-request opens.
`database/sql` handles concurrency within a pool.

### D4: JSON parity quirks

The frontend depends on exact field names (`in`, `out`, `cr`, `cw`,
`reason`, `maxctx`, `dur`, `hu`, `beats`, `subtree`, …). Handlers will
emit structs with exact JSON tags; `omitempty` is banned on fields
that Python always emitted (e.g. `"error": null` in `/api/db` must
stay `null`, not absent). Token counters are emitted as numbers;
message `data` JSON is parsed with `encoding/json` into
`map[string]any` mirroring Python's dict handling. Python's
`True/False` → Go `true/false` for `hu`, `active`, `cut*`, `found`,
`ok`.

### D5: Serving the frontend — go:embed, no edit-and-refresh

Python re-reads `index.html`/`i18n.js` per request so frontend devs can
edit and refresh. The Go binary embeds them. For dev convenience,
`make dev` (or `go run ./cmd/dashboard`) reads from disk when the files exist next
to the binary? No — keep it honest: assets are embedded; frontend
iteration happens via `make dev` which starts the server with a flag
`-dev` that serves from the `src/` directory on disk when present.
The frozen/embedded path needs no `sys._MEIPASS` equivalent.

Alternative considered: keeping per-request disk reads only —
rejected because the whole point is a self-contained binary; the
`-dev` flag covers the edit-and-refresh flow.

### D6: Browser opening

`NO_BROWSER` unset → open the URL after ~0.5s, like Python. Go
per-platform: `xdg-open` (linux), `open` (darwin), `rundll32 url.dll,FileProtocolHandler`
or `start` via `cmd /c` (windows). No third-party dependency; a tiny
switch on `runtime.GOOS`. Failure to open the browser is non-fatal
(log to stderr).

### D7: Port fallback

Bind `127.0.0.1:PORT+i` for i in 0..19 until one listens; report the
substitution exactly like Python ("Port 8787 already in use — using
8789 instead."). All 20 failures → print guidance and exit non-zero.

### D8: Build matrix in the Makefile

```
TARGETS = lin-x64/amd64/linux lin-arm64/arm64/linux mac-darwin/arm64/darwin \
          mac-darwin-amd64/amd64/darwin windows/amd64/windows
```

`make build` → host platform only (name derived from GOOS/GOARCH:
`dashboard-lin-x64`, `dashboard-mac-darwin`, `dashboard.exe`;
darwin-amd64 gets `-amd64` suffix, linux-arm64 gets `-arm64`).
`make build-all` → loop over the matrix with
`GOOS=… GOARCH=… CGO_ENABLED=0 go build -trimpath -ldflags "-s -w"`.
`.exe` appended for windows. venv/PyInstaller machinery, `src/build.bat`
and the old `test/test_dashboard.py` are deleted.

### D9: Tests

Port `test/test_dashboard.py` to Go table-driven tests
(`*_test.go`): spin up an in-memory server (`httptest`) against a
fixture sqlite DB, assert route status codes and JSON shapes
(deep-compare via `json.Unmarshal` into maps, plus targeted struct
checks). The existing Node tests keep running against the Go server
(`make test` = `go test ./... && node --test …`). The Python test file
and Python itself leave the project.

### D10: Version

Single `const Version = "3.0.0"` in `main.go`, substituted into
`index.html` on serve. Major bump because the distribution/runtime
contract changes (binary-only), while the web API itself is unchanged.

## Risks / Trade-offs

- [modernc.org/sqlite is slower than CGO sqlite] → This dashboard is a
  local, single-user read-only tool; query volume is trivial.
  Acceptable.
- [Behavior drift in JSON edge cases (null vs missing, number
  formatting)] → Ported tests assert exact shapes; deep-compare
  helper surfaces any drift before merge.
- [modernc URI handling quirks vs python sqlite3 `mode=ro`] → Covered
  by a dedicated validation test with odd paths (spaces, unicode);
  fix-forward in the db layer if needed.
- [Tree/subtree recursion parity for deep nesting] → Ported tests
  include a multi-level fixture mirroring the Python suite.
- [macOS binaries unsigned] → unchanged from today; README already
  documents Gatekeeper workarounds.

## Migration Plan

Single-repo, single-user tool — no staged rollout. The change lands
as one commit sequence (tasks.md order): Go implementation + tests
green, then Makefile/README switch, then Python removal. Rollback =
`git revert`; the old artifacts in `dist/` keep working regardless.

## Open Questions

None blocking. (Whether to keep publishing linux-arm64 /
darwin-amd64 binaries long-term is a distribution question that does
not affect this change — the build matrix makes adding/removing a
target a one-line change.)
