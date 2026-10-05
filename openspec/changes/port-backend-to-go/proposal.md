# Proposal: port-backend-to-go

## Why

The dashboard backend is Python, which makes distribution depend on
PyInstaller. PyInstaller cannot cross-compile, so every user must build
the binary on their own OS/arch, and Windows users need the separate
`src/build.bat`. Porting the backend to Go lets us build binaries for
every target platform (linux/amd64, darwin/arm64, windows/amd64, …)
from a single laptop with plain `go build`, producing small,
dependency-free executables.

## What Changes

- **BREAKING**: Replace the Python backend (`dashboard.py`, PyInstaller
  venv build, `src/build.bat`, Python tests) with a Go implementation.
  The Python entrypoint and `make build` PyInstaller flow are removed;
  Go becomes the only implementation.
- New Go module serving the same HTTP API (`/api/tree`, `/api/calls`,
  `/api/db`, and any other endpoints the frontend consumes) with
  `net/http`, reading the opencode sqlite database via a pure-Go
  sqlite driver (no CGO), and embedding the existing frontend
  (`src/index.html`, `src/i18n.js`) with `go:embed`.
- New `make build` / `make build-all` that cross-compile all targets
  from any host and produce the same artifact names as today:
  `dashboard-lin-x64`, `dashboard-mac-darwin`, `dashboard.exe`
  (plus darwin-amd64 and linux-arm64 variants).
- Port the 30 Python backend tests (`test/test_dashboard.py`) to Go
  tests. The 72 Node frontend tests remain unchanged and must still
  pass against the Go backend.
- Frontend files and behavior are unchanged: same UI, same API
  responses (including localized empty states and error payloads the
  frontend tests exercise indirectly).

## Capabilities

### New Capabilities

- `go-backend`: The Go HTTP server that reads the opencode sqlite
  database, exposes the dashboard API, embeds and serves the frontend
  assets, autodetects the database location, and persists user choices
  (e.g. selected db path) — the behavioral replacement for
  `dashboard.py`.

### Modified Capabilities

(none — `openspec/specs/` is empty; previous capabilities were archived
before this change. `go-backend` is introduced as the first live spec.)

## Impact

- **Removed**: `dashboard.py`, `test/test_dashboard.py`, `src/build.bat`,
  `.buildvenv` machinery in the Makefile.
- **Added**: `go.mod` / Go source (cmd/dashboard entrypoint, src/dashboard
  library, src webui embed package), Go test files, `go:embed` of
  `src/index.html` and `src/i18n.js`.
- **Changed**: `Makefile` (build targets become `go build` with
  GOOS/GOARCH matrix), `README.md` (build instructions, Python
  prerequisites section removed), `.gitignore` (Go artifacts).
- **Dependencies**: one new Go dependency on a pure-Go sqlite driver
  (`modernc.org/sqlite`); Python is no longer needed at build or run
  time. Node stays for frontend tests.
- **Not affected**: `src/index.html`, `src/i18n.js`, all `test/*.mjs`
  frontend tests.
