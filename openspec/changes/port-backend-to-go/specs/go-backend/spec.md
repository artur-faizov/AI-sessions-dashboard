# Delta: go-backend

## Purpose

A single-binary, read-only web dashboard backend in Go that reads the
opencode sqlite database, exposes the dashboard API to the existing
frontend, and cross-compiles to every supported platform from any host.

## ADDED Requirements

### Requirement: Serve the dashboard frontend
The system SHALL serve the embedded frontend assets: `src/index.html`
(with the `{{VERSION}}` placeholder substituted by the current version)
at `/` and `src/i18n.js` at `/i18n.js`, both with `Cache-Control:
no-store` and UTF-8 content types.

#### Scenario: Root page
- **WHEN** a GET request is made to `/`
- **THEN** the response is 200, `text/html; charset=utf-8`, contains
  the full `src/index.html` content with the version number in place
  of `{{VERSION}}`, and is not cached

#### Scenario: i18n script
- **WHEN** a GET request is made to `/i18n.js`
- **THEN** the response is 200, `application/javascript; charset=utf-8`,
  and contains the current `src/i18n.js` content

#### Scenario: Unknown path
- **WHEN** a GET request is made to any other non-API path
- **THEN** the response is 404

### Requirement: Database autodetection and candidates
The system SHALL resolve the opencode database at startup by checking,
in order and deduplicated: the `--db PATH` / `--db=PATH` CLI flag, the
`DB_PATH` environment variable, `$XDG_DATA_HOME/opencode/opencode.db`
(defaulting XDG to `~/.local/share`), and on macOS
`~/Library/Application Support/opencode/opencode.db`, and on Windows
`%APPDATA%\opencode\opencode.db` and `%LOCALAPPDATA%\opencode\opencode.db`.
A candidate is valid only if it exists, opens read-only, and contains
the `session` table. The first valid candidate becomes the active DB;
if none is valid the first candidate remains the reported path with
`found=false`.

#### Scenario: Startup with a valid default location
- **WHEN** the server starts and `$XDG_DATA_HOME/opencode/opencode.db`
  is a valid opencode database
- **THEN** it becomes the active database without any flags

#### Scenario: CLI flag wins
- **WHEN** the server starts with `--db /some/path.db` and that path is
  valid
- **THEN** that path is the active database regardless of defaults

#### Scenario: No valid candidate
- **WHEN** no candidate path is a valid opencode database
- **THEN** the server still starts, reports the first candidate path
  with `found=false`, and the UI shows its database-picker state

### Requirement: Database info endpoint
The system SHALL expose `GET /api/db` returning JSON with the active
`path`, a `found` boolean, the validation `error` (or null), and the
full ordered `candidates` list (including the CLI flag path when
present). Responses to API endpoints SHALL have
`Content-Type: application/json` and `Cache-Control: no-store`.

#### Scenario: Healthy database
- **WHEN** the active database is valid
- **THEN** `GET /api/db` returns 200 with `found: true`, `error: null`
  and the candidates list

#### Scenario: Database vanished at runtime
- **WHEN** the active database file is deleted and `GET /api/db` is
  called
- **THEN** the response reports `found: false` with a descriptive
  `error`

### Requirement: Database switching
The system SHALL expose `POST /api/db` accepting a JSON body
`{"path": "..."}`. If the path validates, the active database switches
for subsequent requests and the response is `{"ok": true, "path": ...}`.
If not, the response is 400 with `{"ok": false, "path": ...,
"error": ...}` and the active database is unchanged. Malformed JSON
bodies get 400 with an `invalid JSON body` error. Other POST paths
return 404.

#### Scenario: Switch to a valid database
- **WHEN** `POST /api/db` sends a valid opencode database path
- **THEN** the response is 200 with `ok: true` and later API requests
  read the new database

#### Scenario: Switch to an invalid path
- **WHEN** `POST /api/db` sends a path that is missing or not an
  opencode database
- **THEN** the response is 400 with `ok: false` and an `error` message,
  and the previously active database keeps serving

### Requirement: Session tree endpoint
The system SHALL expose `GET /api/tree` returning a JSON array of root
session nodes. Only non-archived sessions with a non-empty directory
are included. Each node carries `id`, `parent`, `title` ("(untitled)"
when null), `agent`, `model` (parsed JSON object when the stored value
is valid JSON, plain string otherwise), `dir`, `maxctx`, the token
counters `in`/`out`/`reason`/`cr`/`cw`, `created`, `updated`, `active`
(true when `updated` is within the last 15 minutes), `dur` (clamped at
0), `beats` (sorted timestamps of the session's own assistant calls
with token usage in the active window; empty when inactive), a
recursive `children` array (sorted by `created` descending), and a
`subtree` object summing the five token counters over the node and all
descendants. Roots are sorted by `created` descending. When the active
database is invalid, the endpoint returns 503 with
`{"error": "db_not_found", "path": ...}`. Database errors return 500
with an `error` message.

#### Scenario: Sessions rendered as a tree
- **WHEN** the database contains a parent session and a child
  subagent session, both non-archived with directories
- **THEN** `GET /api/tree` returns the parent as the sole root with
  the child nested in its `children`, and each node's `subtree`
  equals the sum of its own and descendants' counters

#### Scenario: Archived or directory-less sessions excluded
- **WHEN** a session is archived or has an empty `directory`
- **THEN** it does not appear anywhere in the tree response

#### Scenario: Active session with heartbeat
- **WHEN** a session's last update is under 15 minutes ago and it has
  assistant messages with token usage in that window
- **THEN** its node has `active: true` and non-empty sorted `beats`

#### Scenario: Database invalid
- **WHEN** the active database is not valid and `GET /api/tree` is
  called
- **THEN** the response is 503 with `db_not_found`

### Requirement: Session prompt endpoint
The system SHALL expose `GET /api/prompt/<session_id>` returning
`{"texts": [...]}`: the session's user-message text parts, in message
order, that are non-empty, not synthetic, and not
`<system-reminder>`-prefixed, stopping once the collected texts exceed
20000 characters.

#### Scenario: Prompt extraction
- **WHEN** the session's first user message has a plain text part
  followed by a synthetic reminder part
- **THEN** only the real text is returned, in order

### Requirement: Session calls endpoint
The system SHALL expose `GET /api/calls/<session_id>` returning
`{"calls": [...]}`: one record per assistant message with any token
usage, in message order, each carrying `mid`, `time`, `in`, `cr`, `cw`,
`out`, `reason`, `ctx` (input + cache read + write), and `hu` — true
when the immediately preceding message is a user message containing
real human text (non-synthetic, not a system reminder).

#### Scenario: Calls with human trigger flag
- **WHEN** an assistant message with tokens is preceded by a human
  user message, and another is preceded by a tool-result user message
- **THEN** the first call has `hu: true` and the second `hu: false`

### Requirement: Call trigger endpoint
The system SHALL expose `GET /api/trigger/<message_id>` returning the
content of the message immediately preceding the given assistant
message. The response's `case` is `start` (no preceding message),
`user`, or `assistant`; `parts` lists text parts (`kind: "text"`,
`text`, `cut` flag) and tool parts (`kind: "tool"`, `tool`, `status`,
`input`, `output`, `cut_i`, `cut_o`), where non-string values are
rendered as indented JSON and any string over 60000 characters is
truncated with `cut: true`. A message id that does not exist, or that
is not an assistant message, returns 404 with
`{"error": "message not found"}`.

#### Scenario: Trigger with long input
- **WHEN** the preceding message is a user message whose text part
  exceeds 60000 characters
- **THEN** the returned part has the text truncated to 60000
  characters and `cut: true`

#### Scenario: Unknown message
- **WHEN** the endpoint is called with a nonexistent message id
- **THEN** the response is 404 with `error: "message not found"`

### Requirement: Startup and port behavior
The system SHALL listen on `127.0.0.1` at the port from the `PORT`
environment variable (default 8787). If that port is in use, it SHALL
try successive ports up to 20 attempts before failing with a clear
message. On startup it prints the serving URL and the active database
path; when the database is not found it prints a warning but still
starts. Unless `NO_BROWSER` is set, it opens the dashboard URL in the
system browser shortly after startup. `--version` prints the version
and exits. Ctrl-C shuts it down gracefully.

#### Scenario: Port fallback
- **WHEN** port 8787 is already occupied and 8789 is free
- **THEN** the server binds 8789 and reports that in the startup output

#### Scenario: Version flag
- **WHEN** the binary is run with `--version`
- **THEN** it prints the version string and exits without serving

### Requirement: Read-only database access
The system SHALL open the sqlite database in read-only mode for every
query and SHALL never write to it.

#### Scenario: Database file untouched
- **WHEN** the dashboard serves requests against a database
- **THEN** the database file's mtime and contents are unchanged

### Requirement: Cross-platform builds
The build system SHALL produce, from any host OS with the Go
toolchain, the artifacts `dashboard-lin-x64` (linux/amd64),
`dashboard-lin-arm64` (linux/arm64), `dashboard-mac-darwin`
(darwin/arm64), `dashboard-mac-darwin-amd64` (darwin/amd64) and
`dashboard.exe` (windows/amd64), via `make build` (current platform)
and `make build-all` (all listed targets). No Python, PyInstaller or
virtualenv is required at build time.

#### Scenario: Cross-compile from Linux
- **WHEN** `make build-all` runs on a Linux machine
- **THEN** all five binaries appear in `dist/` and each runs on its
  target platform

#### Scenario: Single-platform build
- **WHEN** `make build` runs on any platform
- **THEN** only the binary matching the host platform is produced
