// The Herdr panel's scanner (herdr/mods/ai-sessions/scan.js) on a fixture home:
// sessions, subagents, Codex children, context, titles, beats and calls; and that an
// incremental rescan over appended bytes gives what a fresh scan of the same files does.
import { test } from "node:test";
import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import * as fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { Scanner } from "../herdr/mods/ai-sessions/scan.js";

if (process.platform === "win32") test.skip("scan.js lists files with find/stat (Unix)");

const io = (home) => ({
  platform: process.platform === "darwin" ? "macos" : "linux",
  home: () => home,
  exec: (cmd, args) => new Promise((res, rej) => execFile(cmd, cmd === "printenv" ? ["__AI_SESSIONS_NONE__"] : args,
    { maxBuffer: 8 << 20, encoding: "utf8" }, (e, stdout, stderr) => (e && typeof e.code !== "number" ? rej(e) : res({ code: e ? e.code : 0, stdout, stderr })))),
  readTextFile: (p) => fs.readFile(p, "utf8").catch(() => null),
  readdir: (p) => fs.readdir(p, { withFileTypes: true }),
  exists: (p) => fs.access(p).then(() => true, () => false),
});

const iso = (ms) => new Date(ms).toISOString();
const L = (o) => JSON.stringify(o) + "\n";

async function fixture() {
  const home = await fs.mkdtemp(path.join(os.tmpdir(), "ai-sessions-"));
  const proj = path.join(home, ".claude", "projects", "-home-me-app");
  const sid = "11111111-aaaa";
  await fs.mkdir(path.join(proj, sid, "subagents"), { recursive: true });
  const t = Date.now() - 600000;
  const main = path.join(proj, `${sid}.jsonl`);
  await fs.writeFile(main,
    L({ type: "user", timestamp: iso(t), message: { role: "user", content: "fix the bug — ünïcode ✓" } }) +
    L({ type: "assistant", timestamp: iso(t + 2000), requestId: "r1", message: { model: "m", usage: { input_tokens: 10, cache_read_input_tokens: 1000, cache_creation_input_tokens: 5 }, content: [{ type: "tool_use", name: "Bash" }] } }) +
    L({ type: "ai-title", aiTitle: "Fix the bug" }));
  const agent = path.join(proj, sid, "subagents", "agent-abc.jsonl");
  await fs.writeFile(agent,
    L({ type: "user", timestamp: iso(t + 3000), message: { content: "sub task" } }) +
    L({ type: "assistant", timestamp: iso(t + 4000), requestId: "s1", message: { model: "m", usage: { input_tokens: 500 }, content: [{ type: "text", text: "done" }] } }));
  await fs.writeFile(path.join(proj, sid, "subagents", "agent-abc.meta.json"), JSON.stringify({ agentType: "Explore", description: "Look around", finished: true }));
  const cdir = path.join(home, ".codex", "sessions", "2026", "11", "15");
  await fs.mkdir(cdir, { recursive: true });
  const roll = path.join(cdir, "rollout-x.jsonl");
  await fs.writeFile(roll,
    L({ type: "session_meta", timestamp: iso(t), payload: { id: "th1", cwd: "/home/me/app" } }) +
    L({ type: "event_msg", timestamp: iso(t + 100), payload: { type: "task_started" } }) +
    L({ type: "response_item", timestamp: iso(t + 200), payload: { type: "message", role: "user", content: [{ type: "input_text", text: "codex prompt\nmore" }] } }) +
    L({ type: "response_item", timestamp: iso(t + 300), payload: { type: "function_call", name: "shell" } }) +
    L({ type: "token_usage_record", timestamp: iso(t + 900), payload: { thread_id: "th1", response_id: "a", usage: { input_tokens: 777 } } }));
  return { home, main, agent, roll, t };
}

test("scan: sessions, subagents, codex, ctx, titles, beats", async () => {
  const f = await fixture();
  const d = await new Scanner(io(f.home)).scan(3);
  assert.deepEqual(d.dirs, ["-home-me-app"]);
  const c = d.sessions.find((s) => s.provider === "claude");
  assert.equal(c.title, "Fix the bug");
  assert.equal(c.project, "app");
  assert.equal(c.ctx, 1015);
  assert.deepEqual(c.beats, [[f.t, f.t + 2000]]);
  assert.equal(c.subagents.length, 1);
  assert.deepEqual([c.subagents[0].id, c.subagents[0].type, c.subagents[0].description, c.subagents[0].ctx], ["abc", "Explore", "Look around", 500]);
  const x = d.sessions.find((s) => s.provider === "codex");
  assert.deepEqual([x.id, x.title, x.ctx, x.dir], ["th1", "codex prompt", 777, "-home-me-app"]);
  assert.deepEqual(x.beats, [[f.t + 200, f.t + 900]]);
});

test("scan: appended bytes give what a fresh scan gives", async () => {
  const f = await fixture();
  const sc = new Scanner(io(f.home));
  await sc.scan(3);
  // a half-written line first: it must wait for its newline
  const rest = L({ type: "assistant", timestamp: iso(f.t + 9000), requestId: "r2", message: { model: "m", usage: { input_tokens: 2000 }, content: [] } });
  await fs.appendFile(f.main, L({ type: "user", timestamp: iso(f.t + 8000), message: { content: "again ✓" } }) + rest.slice(0, 20));
  await sc.scan(3);
  await fs.appendFile(f.main, rest.slice(20));
  const inc = await sc.scan(3);
  const full = await new Scanner(io(f.home)).scan(3);
  assert.deepEqual(inc, full);
  assert.equal(inc.sessions.find((s) => s.provider === "claude").ctx, 2000);
});

test("calls: newest first with tools, trigger and delta", async () => {
  const f = await fixture();
  const sc = new Scanner(io(f.home));
  await sc.scan(3);
  const calls = await sc.calls(f.main);
  assert.equal(calls.length, 1);
  assert.deepEqual([calls[0].by, calls[0].tools, calls[0].ctx, calls[0].delta], ["prompt", ["Bash"], 1015, 1015]);
  const cx = await sc.calls(f.roll);
  assert.deepEqual([cx[0].tools, cx[0].ctx, cx[0].sent], [["shell"], 777, f.t + 200]);
});
