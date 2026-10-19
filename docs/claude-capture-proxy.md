# Claude Code capture proxy (plan, not implemented)

## Why

The "Context composition" pie (call #1 of a session, `/api/context/<mid>`)
splits the first call's input context into what the transcript stores:
system prompt text, CLAUDE.md, skills and agents lists, hook output,
reminders, the user prompt. The tool schemas (built-in and MCP tools) are
never stored, so they show as one hatched "not recorded" slice — about 80% of
a typical Claude Code first call (~25k of ~31k tokens). For opencode almost the
whole context is hidden.

Asking the user to run `/context` by hand is not an option. The only way to
see the real request is to sit between Claude Code and its API and copy what
it sends.

## Goal

Exact `system` and `tools` of every Claude Code request, so the dashboard can
replace the hatched slice with per-tool / per-MCP-server sizes. Claude Code
only; opencode is out of scope for now.

## Hard requirement: never break Claude Code

- No global config is rewritten (no `settings.json`, no `.bashrc` edits by the
  tool), so there is nothing to restore after a crash, `kill -9` or reboot.
- A new session never starts against a dead proxy.
- Capturing never blocks or changes a request.

Rejected: "replace ANTHROPIC_BASE_URL globally, restore it when the tool
stops". A crash skips the restore and every new session fails; and sessions
already running keep the proxy address (Claude Code reads it once at start),
so they break when the proxy stops regardless of the restore.

## Design

### 1. Upstream comes from the launch environment, not from files

The user sets the upstream per terminal, e.g.
`export ANTHROPIC_BASE_URL="https://ai-llm-proxy.k8s.n3.2gis.io/anthropic"`.
The proxy must use exactly that end value, so the upstream travels inside the
proxy URL:

```
ANTHROPIC_BASE_URL=http://127.0.0.1:9000/u/<base64url(original base URL)>
```

Claude Code keeps the base path and appends `/v1/messages`; the proxy decodes
`<enc>` and forwards to `<original>/v1/messages`. The proxy keeps no upstream
config, different terminals may use different upstreams at once, and a proxy
restart loses nothing.

### 2. Launch wrapper (shell function)

Applies the proxy only to the launched process; the terminal's exported value
stays untouched. If the proxy does not answer, Claude Code starts exactly as
today.

```bash
claude() {
  local up="${ANTHROPIC_BASE_URL:-https://api.anthropic.com}"
  if curl -fsm 0.2 http://127.0.0.1:9000/healthz >/dev/null 2>&1; then
    ANTHROPIC_BASE_URL="http://127.0.0.1:9000/u/$(printf %s "$up" | base64 -w0 | tr '+/' '-_' | tr -d =)" command claude "$@"
  else
    command claude "$@"
  fi
}
```

The user adds it to their shell rc themselves; disabling = removing it.

### 3. Proxy process (`dashboard capture`)

- Separate process from the dashboard UI, so restarting the dashboard never
  touches running Claude Code sessions.
- Listens on `127.0.0.1` only; `GET /healthz` for the wrapper.
- Pure pass-through (`httputil.ReverseProxy`): headers and body forwarded as
  is, the auth header (`x-api-key` / `Authorization`) included; responses
  streamed back unbuffered (SSE, `FlushInterval: -1`).
- Capture: copy the request body, hand it to a buffered channel; a background
  worker parses it and stores only `system` and `tools`. If the channel is
  full or parsing fails, drop silently.
- Storage: content-addressed blobs (`sha256 -> json`) plus one small record
  per request (time, model, session id, hashes of system / each tool). Auth
  headers and message history are never stored. Retention limit on records.
- Optionally run as a `systemd --user` service with `Restart=always`
  (open question below).

### 4. Dashboard side

- Read the capture dir; match a capture to a transcript call by session id +
  time.
- In `src/provider/claude/context.go`, when a capture exists for the call,
  replace the inferred `hidden` item with exact items: one per tool (kind
  `tools`, label = tool name, MCP tools grouped by server) and the real system
  prompt; keep the current inferred split as fallback.
- Later: works for any call, not only the first; diff between consecutive
  requests; cache-miss explanation (first changed byte of the prefix).

## Residual risk

If the proxy dies mid-session, in-flight requests fail until it is back
(~1 s under systemd); Claude Code retries connection errors. This is the
minimum exposure for anything in the request path.

## Open questions

1. Proxy lifecycle: `systemd --user` service with auto-restart, or started by
   hand (captures only while it runs)?
2. Matching key: Claude Code is believed to send the session id in the
   request `metadata.user_id`; verify on the first capture.
3. Does the same work with a Claude.ai subscription login (OAuth) and a custom
   base URL? Not needed for the current gateway + API key setup.
4. Port choice and what happens when it is taken.
