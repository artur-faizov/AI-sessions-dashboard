// Claude Code and Codex CLI transcripts active in the last DAYS days, for the panel.
//
// Per transcript: context (input + cache_read + cache_creation of the last real assistant
// message, token-monitor's rule), title, and beats: one [sent, finished] ms pair per
// assistant API call, sent = the last input line before the response, finished = the
// response's last line (an open beat [t, 0] when a live transcript ends on an input line).
// scan() returns {dirs: [every Claude projects dir], sessions: [...]}; every node carries
// its transcript "path", and calls(path) lists that transcript's last API calls.
//
// Incremental: one `find … -exec stat` per scan lists the recent files with size and
// mtime; each file keeps its parse state and byte offset in memory, and only the bytes
// appended since the last scan are read (dd seek + head, in chunks under exec's 8 MiB
// output cap). Unchanged files cost nothing. Nothing is written to disk, so the first
// scan after Herdr starts parses the last DAYS days of transcripts once.
//
// All I/O goes through `io` ({exec, readTextFile, readdir, exists, readFile, home,
// platform, env}) so the same code runs in Herdr and under node for tests.

const LIVE_MS = 5 * 60 * 1000;  // a trailing input line this fresh means a request in flight
const CHUNK = 6 << 20;           // bytes per read, below exec's 8 MiB output limit
const TAIL = 4 << 20, LAST = 200; // calls(): only the file's last TAIL bytes, newest LAST calls

const ms = (ts) => { const t = typeof ts === "string" ? Date.parse(ts) : NaN; return Number.isFinite(t) ? t : 0; };
const base = (p) => p.slice(p.replace(/\\/g, "/").lastIndexOf("/") + 1);
const dirname = (p) => p.slice(0, p.replace(/\\/g, "/").lastIndexOf("/"));
const join = (...a) => a.join("/");
const enc = (s) => s.replace(/[^A-Za-z0-9]/g, "-");
const json = (s) => { try { return JSON.parse(s); } catch { return null; } };

// UTF-8 byte length of a JS string (what the consumed text took in the file).
function bytes(s) {
  let n = 0;
  for (let i = 0; i < s.length; i++) {
    const c = s.charCodeAt(i);
    if (c < 0x80) n += 1;
    else if (c < 0x800) n += 2;
    else if (c >= 0xd800 && c < 0xdc00) { n += 4; i++; }
    else n += 3;
  }
  return n;
}

const fresh = () => ({ off: 0, size: 0, mtime: 0, ctx: 0, model: "", title: "", prompt: "", created: 0,
  beats: [], rid: "", last_in: 0, last_role: "", last_ts: 0 });
const cfresh = () => ({ off: 0, size: 0, mtime: 0, id: "", parent: "", cwd: "", agent: "", prompt: "",
  model: "", ctx: 0, beats: [], last_in: 0, busy: false, last_ts: 0, created: 0, rich: false, total: null, resp: [] });

const realPrompt = (t) => { t = t.trim(); return !!t && !/^(<environment_context>|# AGENTS\.md instructions|<INSTRUCTIONS>|<instructions>)/.test(t); };

// One Claude transcript line into its cached state c.
function claudeLine(c, raw, lo) {
  if (!raw.includes('"usage"') && !raw.includes('"type":"user"') && !raw.includes('title"') && !raw.includes('"last-prompt"')) return;
  const d = json(raw);
  if (!d || typeof d !== "object") return;
  const t = d.type;
  if (t === "custom-title") { c.title = d.customTitle || c.title; return; }
  if (t === "ai-title") { c.title = d.aiTitle || c.title; return; }
  if (t === "last-prompt") { c.prompt = d.lastPrompt || c.prompt; return; }
  if ((t !== "user" && t !== "assistant") || !d.message) return;
  const ts = ms(d.timestamp);
  if (!ts) return;
  c.created ||= ts;
  c.last_role = t; c.last_ts = ts;
  if (t === "user") { c.last_in = ts; return; }
  const m = d.message, beats = c.beats;
  const rid = d.requestId || d.uuid || "";
  if (rid && rid === c.rid && beats.length) {  // one request, several lines
    beats[beats.length - 1][1] = Math.max(beats[beats.length - 1][1], ts);
    return;
  }
  if (m.model === "<synthetic>") return;
  const u = m.usage;
  if (u) {
    const ctx = (u.input_tokens || 0) + (u.cache_read_input_tokens || 0) + (u.cache_creation_input_tokens || 0);
    if (ctx) { c.ctx = ctx; c.model = m.model || c.model; }
  }
  c.rid = rid;
  const li = c.last_in;
  beats.push([li && li < ts ? li : ts, ts]);
}

function ccall(c, ts, inp) {
  c.ctx = inp;
  const li = c.last_in;
  c.beats.push([li && li < ts ? li : ts, ts]);
}

// One Codex rollout line (token-monitor's codex provider rules): one call per unique
// token_usage_record response_id of this thread (event_msg/token_count only for files
// without such records, one per advancing cumulative total); context = input_tokens of the
// last call; children nest under session_meta's parent_thread_id.
function codexLine(c, raw) {
  if (!raw) return;
  const d = json(raw);
  if (!d || typeof d !== "object") return;
  const t = d.type, p = d.payload || {};
  if (typeof p !== "object" || Array.isArray(p)) return;
  const ts = ms(d.timestamp);
  if (ts) { c.created ||= ts; c.last_ts = Math.max(c.last_ts, ts); }
  const pt = p.type;
  if (t === "session_meta") {
    c.id = p.id || c.id;
    c.cwd = p.cwd || c.cwd;
    c.created = ms(p.timestamp) || c.created;
    const src = p.source;
    const sp = (src && typeof src === "object" && (src.subagent || {}).thread_spawn) || {};
    c.parent = p.parent_thread_id || sp.parent_thread_id || "";
    const ap = p.agent_path || sp.agent_path || "";
    c.agent = p.agent_role || sp.agent_role || p.agent_nickname || sp.agent_nickname || (ap ? base(ap) : "");
  } else if (t === "turn_context") {
    c.model = p.model || c.model;
    c.cwd = c.cwd || p.cwd || "";
  } else if (t === "token_usage_record") {
    const tid = p.thread_id;
    if (c.id && tid && tid !== c.id) return;  // a fork's inherited history
    c.id = c.id || tid || "";
    const u = p.usage, rid = p.response_id || "";
    if (!u || (rid && c.resp.includes(rid))) return;
    c.rich = true;
    if (rid) c.resp = [...c.resp, rid].slice(-50);
    ccall(c, ts, u.input_tokens || 0);
  } else if (t === "event_msg" && pt === "token_count") {
    const info = p.info || {}, tot = info.total_token_usage;
    if (c.rich || !tot || (c.total && JSON.stringify(tot) === JSON.stringify(c.total))) return;
    const last = info.last_token_usage || {};
    let inp;
    if (c.total && (tot.total_tokens || 0) >= (c.total.total_tokens || 0) && !Object.keys(last).length)
      inp = (tot.input_tokens || 0) - (c.total.input_tokens || 0);
    else inp = last.input_tokens || 0;
    c.total = tot;
    ccall(c, ts, inp);
  } else if (t === "event_msg" && pt === "task_started") {
    c.busy = true;
    c.last_in = p.started_at || ts;
  } else if (t === "event_msg" && (pt === "task_complete" || pt === "turn_aborted")) {
    c.busy = false;
  } else if (t === "response_item" && (pt === "function_call_output" || pt === "custom_tool_call_output")) {
    c.last_in = ts || c.last_in;
  } else if (t === "response_item" && pt === "message" && p.role === "user") {
    const texts = (p.content || []).filter((x) => x && typeof x === "object" && x.type === "input_text" && realPrompt(x.text || "")).map((x) => x.text || "");
    if (texts.length) {
      c.last_in = ts || c.last_in;
      if (!c.prompt) c.prompt = texts[0].trim().split("\n", 1)[0].slice(0, 80);
    }
  }
}

export class Scanner {
  constructor(io) {
    this.io = io;
    this.files = new Map();  // path -> parse state
    this.metas = new Map();  // finished subagent's meta path -> meta (final, never re-read)
    this.stats = new Map();  // path -> {size, mtime} from the last listing
    this.callsCache = null;  // {path, size, mtime, calls}
    this.lastRead = 0;       // bytes read by the last scan
  }

  async roots() {
    if (this.root) return;
    const io = this.io, h = io.home().replace(/\\/g, "/");
    this.root = join(h, ".claude", "projects");
    let ch = "";
    if (io.platform !== "windows") {
      try { ch = (await io.exec("printenv", ["CODEX_HOME"])).stdout.trim(); } catch {}
    }
    this.codex = (ch || join(h, ".codex")).replace(/\\/g, "/");
  }

  // Every *.jsonl under the two roots modified in the last `mins` minutes: path -> {size, mtime (s)}.
  async list(mins) {
    const io = this.io, roots = [];
    for (const r of [this.root, join(this.codex, "sessions")]) if (await io.exists(r)) roots.push(r);
    const out = new Map();
    if (!roots.length) return out;
    let r;
    if (io.platform === "windows") {
      const ps = `$t=(Get-Date).AddMinutes(-${mins}); foreach($r in $args){ Get-ChildItem -LiteralPath $r -Recurse -File -Filter *.jsonl -ErrorAction SilentlyContinue | Where-Object { $_.LastWriteTime -ge $t } | ForEach-Object { '{0} {1} {2}' -f [int64](($_.LastWriteTimeUtc - [datetime]'1970-01-01').TotalSeconds), $_.Length, $_.FullName } }`;
      r = await io.exec("powershell", ["-NoProfile", "-NonInteractive", "-Command", ps, ...roots]);
    } else {
      const fmt = io.platform === "macos" ? ["-f", "%m %z %N"] : ["-c", "%Y %s %n"];
      r = await io.exec("find", [...roots, "-name", "*.jsonl", "-mmin", `-${mins}`, "-exec", "stat", ...fmt, "{}", "+"], { timeout: 20000 });
    }
    for (const line of r.stdout.split(/\r?\n/)) {
      const m = line.match(/^(\d+) (\d+) (.+)$/);
      if (m) out.set(m[3].replace(/\\/g, "/"), { mtime: Number(m[1]), size: Number(m[2]) });
    }
    return out;
  }

  // The file's text from byte `off` (up to CHUNK bytes; the last line may be partial).
  async readFrom(path, off, len = CHUNK) {
    const io = this.io;
    if (io.platform === "windows") {
      const b = await io.readFile(path);
      return io.decode(b.subarray(off, off + len));
    }
    // dd only seeks the shared stdin offset (count=0 copies nothing), head reads from there.
    // Not `tail -c +N`: macOS tail walks the bytes one by one (~20 ns each).
    const r = await io.exec("sh", ["-c", '{ [ "$1" = 0 ] || dd bs=1 skip="$1" count=0 2>/dev/null; head -c "$3"; } < "$2"', "sh", String(off), path, String(len)], { timeout: 20000 });
    return r.stdout;
  }

  // Advance a file's state over the bytes appended since c.off, line by line.
  async advance(path, st, c, reset, line) {
    if (st.size < c.off) reset();
    c = this.files.get(path);
    c.size = st.size; c.mtime = st.mtime;
    while (c.off < st.size) {
      const text = await this.readFrom(path, c.off);
      const end = text.lastIndexOf("\n") + 1;  // leave a half-written last line for next time
      if (!end) {
        if (bytes(text) >= CHUNK) { c.off += bytes(text); continue; }  // a line over 6 MiB: skip it
        break;
      }
      const done = text.slice(0, end);
      c.off += bytes(done);
      this.lastRead += bytes(done);
      for (const raw of done.split("\n")) line(c, raw);
    }
    return c;
  }

  async claudeNode(path, st, lo, now, live) {
    if (!this.files.has(path)) this.files.set(path, fresh());
    let c = this.files.get(path);
    if (c.size !== st.size || c.mtime !== st.mtime) {
      c = await this.advance(path, st, c, () => this.files.set(path, fresh()), (c, raw) => claudeLine(c, raw, lo));
      c.beats = [...c.beats.slice(0, -1).filter((b) => b[1] >= lo), ...c.beats.slice(-1)];  // keep the last, it may still grow
    }
    const beats = c.beats.filter((b) => b[1] >= lo).map((b) => [...b]);
    if (live == null) live = now - st.mtime * 1000 < LIVE_MS;
    if (c.last_role === "user" && live) beats.push([c.last_ts, 0]);
    return [c, beats];
  }

  async meta(path) {
    if (this.metas.has(path)) return this.metas.get(path);
    const m = json((await this.io.readTextFile(path).catch(() => null)) || "") || {};
    if (m.finished === true) this.metas.set(path, m);
    return m;
  }

  async scan(days = 3) {
    await this.roots();
    const now = Date.now(), lo = now - days * 86400000;
    this.lastRead = 0;
    const listed = await this.list(Math.ceil(days * 1440));
    const claude = [], subs = new Map(), codex = [];
    const r = this.root + "/", cx = join(this.codex, "sessions") + "/";
    for (const [p, st] of listed) {
      if (st.mtime * 1000 < lo) continue;
      if (p.startsWith(r)) {
        const rel = p.slice(r.length).split("/");
        if (rel.length === 2) claude.push(p);
        else if (rel.length === 4 && rel[2] === "subagents" && /^agent-.*\.jsonl$/.test(rel[3])) {
          const k = `${rel[0]}/${rel[1]}`;
          if (!subs.has(k)) subs.set(k, []);
          subs.get(k).push(p);
        }
      } else if (p.startsWith(cx) && base(p).startsWith("rollout-")) codex.push(p);
    }
    const sessions = [];
    for (const p of claude) {
      const [c, beats] = await this.claudeNode(p, listed.get(p), lo, now);
      const sid = base(p).slice(0, -6), proj = base(dirname(p));
      const agents = [];
      for (const a of subs.get(`${proj}/${sid}`) || []) {
        const meta = await this.meta(a.slice(0, -6) + ".meta.json");
        const st = listed.get(a);
        const [ac, abeats] = await this.claudeNode(a, st, lo, now, meta.finished === false);
        agents.push({ id: base(a).slice(6, -6), path: a, type: meta.agentType || "", description: meta.description || "",
          finished: meta.finished ?? false, ctx: ac.ctx, model: ac.model, created: ac.created, mtime: st.mtime, beats: abeats });
      }
      agents.sort((x, y) => x.mtime - y.mtime);
      sessions.push({ id: sid, path: p, provider: "claude", dir: proj,
        project: proj.startsWith("-") ? proj.split("-").pop() : proj,
        title: c.title || c.prompt.slice(0, 80) || sid.slice(0, 8),
        ctx: c.ctx, model: c.model, created: c.created, mtime: listed.get(p).mtime, beats, subagents: agents });
    }
    sessions.push(...(await this.codexNodes(codex, listed, lo, now)));
    sessions.sort((a, b) => b.mtime - a.mtime);
    for (const p of this.files.keys()) if (!listed.has(p)) this.files.delete(p);
    this.stats = listed;
    let dirs = [];
    try { dirs = (await this.io.readdir(this.root)).filter((d) => d.isDirectory()).map((d) => d.name); } catch {}
    const known = new Set(dirs);
    dirs.push(...[...new Set(sessions.filter((s) => s.provider === "codex" && s.dir && !known.has(s.dir)).map((s) => s.dir))].sort());
    return { dirs, sessions };
  }

  async codexNodes(paths, listed, lo, now) {
    const nodes = new Map();
    for (const p of paths) {
      const st = listed.get(p);
      if (!this.files.has(p)) this.files.set(p, cfresh());
      let c = this.files.get(p);
      if (c.size !== st.size || c.mtime !== st.mtime) {
        c = await this.advance(p, st, c, () => this.files.set(p, cfresh()), codexLine);
        c.beats = c.beats.filter((b) => b[1] >= lo);
      }
      const sid = c.id || base(p).slice(0, -6);
      const beats = c.beats.filter((b) => b[1] >= lo).map((b) => [...b]);
      const live = c.busy && now - st.mtime * 1000 < LIVE_MS;
      if (live && c.last_in && (!beats.length || c.last_in > beats[beats.length - 1][1])) beats.push([c.last_in, 0]);
      const old = nodes.get(sid);
      if (old && old.mtime >= st.mtime) continue;  // duplicate copy of one thread: keep the fresher
      const cwd = c.cwd;
      nodes.set(sid, { id: sid, provider: "codex", parent: c.parent, path: p, dir: enc(cwd),
        project: base(cwd.replace(/[\\/]+$/, "")) || "codex",
        title: c.prompt || c.agent || (c.parent ? "Subagent" : sid.slice(0, 8)),
        type: c.agent || "subagent", description: c.prompt || c.agent,
        finished: !live, ctx: c.ctx, model: c.model, created: c.created, mtime: st.mtime, beats, subagents: [] });
    }
    const roots = [];
    for (const n of nodes.values()) {
      const par = n.parent !== n.id ? nodes.get(n.parent) : null;
      if (par && !par.parent) par.subagents.push(n);
      else roots.push(n);  // orphans (and deeper levels, kept flat) stay visible as roots
    }
    for (const n of roots) n.subagents.sort((a, b) => a.mtime - b.mtime);
    return roots;
  }

  // One transcript's API calls, newest first, for the calls pane: only the file's last TAIL
  // bytes are read (the first, partial line dropped), and only when the file changed since
  // the last call for the same path. Each call: sent (last input before it), end (its last
  // line, 0 = in flight), ctx, by ("prompt" | "tool"), tools (names used), text (a snippet),
  // delta (ctx minus the previous call's).
  async calls(path) {
    const st = this.stats.get(path) || (await this.list(100000)).get(path);
    if (!st) return [];
    const hit = this.callsCache;
    const now = Date.now(), live = now - st.mtime * 1000 < LIVE_MS;
    if (hit && hit.path === path && hit.size === st.size && hit.mtime === st.mtime && !live) return hit.calls;
    const from = Math.max(0, st.size - TAIL);
    const text = await this.readFrom(path, from, TAIL);
    const out = parseCalls(text.split("\n").slice(st.size > TAIL ? 1 : 0), base(path).startsWith("rollout-"), st.size > TAIL, live);
    this.callsCache = { path, size: st.size, mtime: st.mtime, calls: out };
    return out;
  }
}

const snip = (t) => String(t).split(/\s+/).filter(Boolean).join(" ").slice(0, 120);

export function parseCalls(lines, codex, cut, live) {
  const out = [];
  let lastIn = 0, by = "", pend = { tools: [], text: "" };
  let rid = "", rich = false, busy = false, total = null;
  const resp = new Set();
  const add = (ts, ctx, tools, text) => out.push({ sent: lastIn && lastIn < ts ? lastIn : ts, end: ts, ctx, by, tools, text });
  for (const raw of lines) {
    const d = json(raw);
    if (!d || typeof d !== "object") continue;
    const ts = ms(d.timestamp);
    if (!codex) {
      const t = d.type, m = d.message;
      if ((t !== "user" && t !== "assistant") || !m || typeof m !== "object" || !ts) continue;
      const content = Array.isArray(m.content) ? m.content : [{ type: "text", text: m.content || "" }];
      if (t === "user") {
        lastIn = ts; rid = "";
        by = content.some((b) => b && typeof b === "object" && b.type === "tool_result") ? "tool" : "prompt";
        continue;
      }
      if (m.model === "<synthetic>") continue;
      const r = d.requestId || d.uuid || "";
      if (!(r && r === rid && out.length)) {
        const u = m.usage || {};
        add(ts, (u.input_tokens || 0) + (u.cache_read_input_tokens || 0) + (u.cache_creation_input_tokens || 0), [], "");
        rid = r;
      }
      const c = out[out.length - 1];
      c.end = Math.max(c.end, ts);
      for (const b of content) {
        if (!b || typeof b !== "object") continue;
        if (b.type === "tool_use") c.tools.push(b.name || "?");
        else if (b.type === "text" && String(b.text || "").trim() && !c.text) c.text = snip(b.text);
      }
      continue;
    }
    // Codex: a call closes on its token record; the items before it are what it did.
    const t = d.type, p = d.payload || {};
    if (typeof p !== "object" || Array.isArray(p)) continue;
    const pt = p.type;
    if (t === "token_usage_record") {
      const u = p.usage, r = p.response_id || "";
      if (u && !(r && resp.has(r))) {
        rich = true;
        resp.add(r);
        add(ts, u.input_tokens || 0, pend.tools, pend.text);
        pend = { tools: [], text: "" };
      }
    } else if (t === "event_msg" && pt === "token_count" && !rich) {
      const info = p.info || {}, tot = info.total_token_usage;
      if (tot && JSON.stringify(tot) !== JSON.stringify(total)) {
        total = tot;
        add(ts, (info.last_token_usage || {}).input_tokens || 0, pend.tools, pend.text);
        pend = { tools: [], text: "" };
      }
    } else if (t === "event_msg" && pt === "task_started") {
      busy = true; lastIn = ts; by = "prompt";
    } else if (t === "event_msg" && (pt === "task_complete" || pt === "turn_aborted")) {
      busy = false;
    } else if (t === "response_item" && (pt === "function_call" || pt === "custom_tool_call")) {
      pend.tools.push(p.name || "?");
    } else if (t === "response_item" && (pt === "function_call_output" || pt === "custom_tool_call_output")) {
      lastIn = ts || lastIn; by = "tool";
    } else if (t === "response_item" && pt === "message") {
      const txt = (p.content || []).filter((x) => x && typeof x === "object").map((x) => x.text || "").join(" ");
      if (p.role === "assistant" && txt.trim() && !pend.text) pend.text = snip(txt);
      else if (p.role === "user" && realPrompt(txt)) { lastIn = ts || lastIn; by = "prompt"; }
    }
  }
  out.forEach((c, i) => { c.delta = i ? c.ctx - out[i - 1].ctx : c.ctx; });
  const first = cut ? Math.max(1, out.length - LAST) : out.length - LAST;  // a cut file's first delta is unknown
  const kept = out.slice(Math.max(0, first));
  const last = kept[kept.length - 1];
  if ((codex ? busy : by) && live && lastIn && (!last || lastIn > last.end))
    kept.push({ sent: lastIn, end: 0, ctx: 0, delta: 0, by, tools: [], text: "" });
  return kept.reverse();
}
