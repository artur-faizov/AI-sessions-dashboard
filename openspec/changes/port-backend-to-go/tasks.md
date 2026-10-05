# Tasks: port-backend-to-go

## 1. Module bootstrap

- [x] 1.1 Run `go mod init` for the repo, add `modernc.org/sqlite` via
  `go get`, and verify `go build ./...` succeeds with an empty main.go
- [x] 1.2 Create the file layout from design D1 (main.go, server.go,
  db.go, tree.go, calls.go, assets.go) as skeletons and verify
  `go vet ./...` passes

## 2. Database layer

- [x] 2.1 Implement candidate resolution (CLI `--db`/`--db=`, `DB_PATH`
  env, XDG, macOS, Windows defaults with dedup) and verify unit tests
  cover priority order and dedup
- [x] 2.2 Implement read-only validation (exists, opens `mode=ro`,
  `session` table) and verify a unit test against a fixture DB,
  including a bad-schema file and a nonexistent path
- [x] 2.3 Implement the switchable connection pool (generation counter
  under mutex, atomic current-pool read) and verify a unit test that
  switches DBs and observes the new data plus old-pool closure

## 3. API endpoints

- [x] 3.1 Implement route registration, JSON helper (no-store,
  application/json, exact null-vs-missing semantics per design D4) and
  the `/` and `/i18n.js` asset handlers (go:embed, `{{VERSION}}` swap,
  `-dev` disk mode per design D5); verify handlers' tests assert
  content types, no-store, and version substitution
- [x] 3.2 Implement `GET /api/db` (path, found, error, candidates) and
  verify tests cover healthy, missing-file and vanished-at-runtime
  cases
- [x] 3.3 Implement `POST /api/db` (validate, switch, 400 on invalid
  path or malformed JSON, 404 other POSTs) and verify tests cover
  ok/fail/malformed/unchanged-on-failure
- [x] 3.4 Implement `GET /api/tree` (session_max_contexts, beats,
  active window 15 min, dur clamp, model JSON-or-string, subtree
  sums, created-desc sorting) and verify ported tests match the Python
  suite's tree cases on a multi-level fixture
- [x] 3.5 Implement `GET /api/prompt/<sid>` (real user texts,
  system-reminder and synthetic filtering, 20000-char cutoff) and
  verify ported tests match Python behavior
- [x] 3.6 Implement `GET /api/calls/<sid>` (per-assistant-call usage,
  ctx sum, `hu` flag via preceding human message) and verify ported
  tests cover the hu true/false cases
- [x] 3.7 Implement `GET /api/trigger/<mid>` (start/user/assistant
  cases, text/tool parts, 60000-char clipping with cut flags,
  non-string → indented JSON, 404 for unknown/non-assistant) and verify
  ported tests cover truncation and 404
- [x] 3.8 Implement 503 `db_not_found` gating on all API GETs and 500
  on sqlite errors; verify tests cover the 503 path for tree/calls

## 4. Main entry

- [x] 4.1 Implement `main.go`: `--version`, PORT env default 8787,
  20-attempt port fallback with Python-identical messages, Ctrl-C
  graceful shutdown; verify tests/observable runs cover version flag
  and port fallback (occupy 8787, expect 8788+ message)
- [x] 4.2 Implement browser opening per design D6 (xdg-open / open /
  cmd start, honoring NO_BROWSER, non-fatal failures) and verify a
  NO_BROWSER=1 run starts clean without launch attempts
- [x] 4.3 Run `make dev` equivalent: `go run . -dev` serves edited
  assets from disk; verify editing src/index.html reflects on refresh

## 5. Test migration

- [x] 5.1 Port all 30 cases of `test/test_dashboard.py` into Go
  `*_test.go` files (httptest server + fixture sqlite DB) and verify
  `go test ./...` passes with parity coverage
- [x] 5.2 Update `make test` to `go test ./... && node --test …` and
  verify all 72 Node frontend tests still pass against the Go server
  (`go run .` + node suite)

## 6. Build system and cleanup

- [x] 6.1 Rewrite the Makefile per design D8 (go build matrix,
  build/build-all with the five target names, `make dev`) and verify
  `make build-all` from Linux produces all five binaries in dist/
- [x] 6.2 Smoke-verify cross-builds: run `file` on each artifact and
  launch `dashboard-lin-x64` against the real opencode DB, checking
  the tree renders in the browser
- [x] 6.3 Delete `dashboard.py`, `test/test_dashboard.py`,
  `src/build.bat`, venv machinery; verify no references remain
  (`grep` for pyinstaller/build.bat/dashboard.py in repo) and
  `make test` still green
- [x] 6.4 Update README (build matrix, no Python prerequisites,
  `make dev`, keep Gatekeeper/SmartScreen notes) and `.gitignore`
  (Go artifacts); verify instructions match actual commands
