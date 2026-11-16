# Prefix capture (plan, not implemented)

**Status (2026-11-16):** the capture run below is not built and is not needed
for Claude Code and Codex — both store their request in the session files (see
"Test results"), and the "Context composition" pie now reads it from there:

- Claude Code (`src/provider/claude/context.go`): the first call's
  `requestRef` leads to its `api-request` line and that to the
  `api-request-shape`; the pie gets the real system prompt and one item per
  tool schema (kind `tools`, MCP tools kind `mcp`), with no "not recorded"
  slice. Transcripts without these lines keep the inferred slice.
- Codex (`src/provider/codex/context.go`): system prompt, AGENTS.md, memory,
  skills, the tagged developer blocks and, since 0.164.0, one item per tool
  from `additional_tools`. 0.163.0 sessions keep the inferred slice for the
  tool schemas.
- opencode is unchanged: it stores only user messages.

## Why

The "Context composition" pie (call #1 of a session, `/api/context/<mid>`)
splits the first call's input context into what the transcript stores:
system prompt text, CLAUDE.md, skills and agents lists, hook output,
reminders, the user prompt. The tool schemas (built-in and MCP tools) are
never stored, so they show as one hatched "not recorded" slice — about 80% of
a typical Claude Code first call (~25k of ~31k tokens). For opencode almost the
whole context is hidden.

Asking the user to run `/context` by hand is not an option. The only way to
see the real request is to receive it instead of the API and copy what the
tool sends.

## Test results (2026-11-16, Claude Code 2.1.311, macOS, subscription login)

Tested with a throwaway record-only listener in this repo's folder.

- **Claude Code already stores the request in the transcript.** Since about
  2.1.304 the session `.jsonl` has three extra line types, and they match the
  bytes on the wire (tools and messages identical; `system` lacks only the
  one-line billing header block):
  - `api-request-shape` — full `system` + `tools`, stored once per
    `shapeHash`;
  - `api-request-blob` — one message, stored once per `hash`;
  - `api-request` — one per API call: `params`, `shapeHash`, the messages as
    `base` / `keep` / `tail` (a delta against the previous request),
    `cacheControl` positions, `querySource`.

  So for Claude Code no listener and no test run is needed: the dashboard can
  read the exact prefix of every call of every real session. 130 of the 136
  latest 2.1.311 sessions on this machine have these lines. The capture run
  below stays for Codex, opencode and older Claude Code.
- **Codex stores its prefix too** (checked in rollout files, not against the
  wire). `session_meta.base_instructions` holds the system prompt, the first
  `developer` / `user` messages hold memory, skills, permissions, AGENTS.md
  and the environment block, and since 0.164.0 a `response_item` of type
  `additional_tools` holds every tool schema grouped by namespace (MCP servers
  are `mcp__<server>` namespaces). In a 0.165.0 session these parts add up to
  ~111k characters against 24.7k real input tokens, so nothing large is
  missing. 0.163.0 sessions have no `additional_tools`. So Codex needs no
  capture run either; only opencode is left.
- The record-only run works: the request arrives 0.6 s after start, and on a
  `400` the headless CLI exits in 2 s. No tokens, the key is not needed, and a
  subscription login works with a custom base URL.
- A headless run is not the interactive prefix. `claude -p` sent 41.6 KB,
  interactive 55.4 KB: headless has no `AskUserQuestion`, `EnterPlanMode`,
  `ExitPlanMode`, a different identity line, shorter `Agent` / `Bash`
  descriptions, fewer skills and agents listed. Interactive in a pty works
  (type the prompt, kill after the capture).
- On a `400` the CLI retries with fewer beta features. The last retries send
  every tool with its full schema (59 tools, 150 KB) instead of the deferred
  form (9 tools + a list of names). Only the first request with tools is the
  real one; the retries give the size of each deferred tool for free.
- Requests without tools seen first: `HEAD /api/hello` and a one-token
  `quota` message.
- The session id is in `metadata.user_id` (a JSON string with `session_id`).
- A capture run leaves a transcript in `~/.claude/projects`;
  `--no-session-persistence` avoids it, but only with `-p`.

## Goal

The exact prefix of a fresh session in a chosen folder — `system`, `tools`
and the blocks injected into the first message — split without AI into parts
with an origin and a size. Claude Code first; Codex and opencode through the
same mechanism.

## Hard requirement: never break the user's tools

- No global config is rewritten (no `settings.json`, no shell rc edits). The
  override lives only in the environment of the one process the dashboard
  spawns, so there is nothing to restore after a crash.
- Running sessions are never touched, and nothing stays in the request path
  of a real session.
- The user's own proxy or gateway is not involved: the captured request never
  leaves the machine.

## Design

### 1. Single-shot run

A "Capture prefix" action in the dashboard asks for:

- the folder to test in;
- optional extra env (`KEY=value` lines), remembered per folder.

The backend then:

1. listens on `127.0.0.1:0` (a free port, this run only);
2. spawns the tool headless in that folder with its base URL pointed at the
   listener;
3. records the first request that carries tools;
4. kills the process and closes the listener (also on a timeout).

The folder is the only input because it decides the project part of the
prefix (project `CLAUDE.md`, `.mcp.json`, project skills, agents, settings);
the rest is global to the user. A fresh run there has the same prefix as a new
real session there, so no session has to be resumed.

### 2. Record only, never forward

The listener does not forward anything. It stores the request body and
answers with a non-retryable error, so the tool exits at once. That means:

- zero tokens spent;
- no upstream URL and no API key needed;
- nothing to chain with the user's real proxy.

Auth headers are never stored. Requests without tools (title generation,
quota checks, token counting) get the same error and are ignored.

### 3. Environment

The tool is started through the user's login shell (`$SHELL -lic '…'`), so
exports from the shell rc apply even when the dashboard was started from the
macOS app or the IDE plugin. Order, later wins:

1. login shell environment;
2. extra env from the form — for values the user exports per terminal;
3. the base URL override.

Env matters for the prefix itself (model, feature flags), not for auth.

### 4. Per tool

| Tool        | Headless run        | Base URL override               | Wire format                    |
|-------------|---------------------|---------------------------------|--------------------------------|
| Claude Code | `claude -p "hi"`    | `ANTHROPIC_BASE_URL`            | Anthropic Messages             |
| Codex       | `codex exec "hi"`   | `OPENAI_BASE_URL` (verify)      | OpenAI Responses               |
| opencode    | `opencode run "hi"` | provider `baseURL` (verify how) | depends on the chosen provider |

### 5. Parsing (no AI)

One parser per wire format, not per tool: Anthropic Messages, OpenAI
Responses, OpenAI Chat Completions. For Anthropic Messages:

- `tools[]` — one part per tool (name, description, schema); MCP tools are
  grouped by server from the `mcp__<server>__` name prefix, the rest are
  built-in.
- `system[]` — one part per block; inside a block, sections are cut at
  markdown headings (heuristic, the only non-exact split).
- first message — `<system-reminder>` blocks are told apart by fixed markers:
  `Contents of <path>` gives one part per instruction file with its path, and
  the skills list, agents list, memory and hook output each have their own.

Size is exact in bytes and characters. Tokens are estimated with
`provider.EstTokens`; when the parts are shown for a real call they are scaled
to that call's real context, as the pie does today.

### 6. Storage

One JSON file per capture in the dashboard data dir: time, tool and its
version, model, folder, the extra env keys (not values), and the parsed parts
with their text. The latest capture per tool + folder is the one used.

### 7. Dashboard side

- In the context composition (`src/provider/claude/context.go`, then
  opencode), when a capture exists for the session's tool + folder, replace
  the inferred `hidden` item with exact items: one per tool (kind `tools`,
  label = tool name, MCP tools grouped by server) and the real system prompt.
  The current inferred split stays as the fallback.
- Show the capture date next to the pie, and flag a tool version that differs
  from the session's.

## Limits

- It is the prefix of a fresh session now, not the exact one of a past
  session: the tool version, MCP servers and instruction files may have
  changed since.
- Changes inside a session (an MCP server connected later, tools loaded on
  demand) are not seen.
- Values exported by hand in one terminal are not seen unless typed into the
  extra env box.
- A headless run may not have exactly the interactive prefix (see open
  question 1).

## Rejected

- Always-on pass-through proxy with a `claude()` shell wrapper (the previous
  version of this plan). It sits in the request path of every session, needs a
  lifecycle (`systemd --user`) and must carry the user's upstream inside the
  proxy URL. Its only gain is the exact request of every call; it may come back
  for cache-miss diffs.
- Replacing `ANTHROPIC_BASE_URL` globally and restoring it on stop. A crash
  skips the restore and every new session fails.
- Resuming a fork of a running session through the proxy. The folder already
  gives the same prefix, and a fresh run needs no session id.

## Open questions

1. Claude Code: build the context composition from the transcript's
   `api-request*` lines first; is the capture run still wanted for it at all
   (only sessions older than ~2.1.304 lack them)?
2. Is the capture run worth building for opencode alone, or do we drop it and
   keep the inferred "not recorded" slice there?
3. Which error status makes Codex and opencode exit at once instead of
   retrying, and is a headless run their interactive prefix?
4. Does a headless run load project MCP servers of a folder that was never
   approved interactively?
5. Does the tool send the request at all when the user is logged out (no
   credentials to attach)?
6. Codex: does `OPENAI_BASE_URL` apply with a ChatGPT login? opencode: how to
   override a provider's `baseURL` for one process only.
7. Exact token counts: the estimate, or an optional call to the provider's
   token-counting endpoint (needs the upstream and a key, so off by default).
