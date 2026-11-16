// AI sessions: Claude Code and Codex CLI sessions active in a chosen time span, merged by
// recency, each with its subagents nested below. Data from ./scan.js (in-process, no
// Python); "Full view" opens the token-monitor dashboard (./dashboard.js).
// Top: a window-size slider (5 min..24 h, log) and a 3-day timeline where the selected
// span (the window's width) is dragged; pinned to now at the right edge by default.

import { View, div, image, PathBuilder, Background } from "gpui-kit";
import { v_flex, h_flex, Slider, SliderTrack, SliderIndicator, SliderThumb, SliderState } from "gpui-base";
import { exec, notify, nextCommand, home, theme, focused, spaces, settings, readTextFile } from "herdr";
import * as fs from "fs/promises";
import { Buffer } from "buffer";
import { Scanner } from "./scan.js";
import { fullView } from "./dashboard.js";

const k = (n) => (n >= 1000 ? `${Math.round(n / 1000)}k` : `${n}`);

function ago(sec) {
  const s = Date.now() / 1000 - sec;
  if (s < 60) return "now";
  if (s < 3600) return `${Math.floor(s / 60)}m`;
  if (s < 86400) return `${Math.floor(s / 3600)}h`;
  return `${Math.floor(s / 86400)}d`;
}

// Activity of API calls in the selected span (beats from scan.js, token-monitor's rule):
// each call [sent, finished] is a faint red band from sent to finished across the row,
// an in-flight one (finished = 0) reaches now; overlapping bands merge. The span is
// time-based, so every 5 s refresh shifts finished bands to the left while pinned to now.
const EKG = "#e5484d", DAYS = 3, TL = DAYS * 86400000, MIN = 60000;
const pct = (v) => `${v.toFixed(2)}%`;

function bands(beats, lo, hi, now) {
  const win = hi - lo, minW = 0.5;
  const x = (t) => Math.max(0, Math.min(100, ((t - lo) / win) * 100));
  const out = [];
  for (const [s, e] of [...(beats || [])].sort((a, b) => a[0] - b[0])) {
    if ((e || now) < lo || s > hi) continue;
    const xs = Math.min(x(s), 100 - minW), xe = Math.max(x(e || now), xs + minW);
    const last = out[out.length - 1];
    if (last && xs <= last[1]) last[1] = Math.max(last[1], xe);
    else out.push([xs, xe]);
  }
  return out;
}

function ekg(beats, lo, hi, now) {
  const spans = bands(beats, lo, hi, now);
  if (!spans.length) return null;
  const path = PathBuilder.fill();
  for (const [xs, xe] of spans) path.add_polygon([[pct(xs), "0%"], [pct(xe), "0%"], [pct(xe), "100%"], [pct(xs), "100%"]], true);
  return window.paint_path(path.build(), Background.solid(EKG).opacity(0.32)).absolute().top(0).left(0).size_full();
}

// The window slider runs 0..1000 on a log scale from 5 min to 24 h (token-monitor's
// recWinAt/recSliderPos), minutes rounded coarser as they grow.
const REC_MIN = 5, REC_MAX = 24 * 60;
const recWinAt = (pos) => {
  const m = REC_MIN * Math.pow(REC_MAX / REC_MIN, pos / 1000), step = m < 60 ? 1 : m < 360 ? 5 : 15;
  return Math.min(REC_MAX, Math.max(REC_MIN, Math.round(m / step) * step));
};
const recSliderPos = (min) => Math.round((Math.log(min / REC_MIN) / Math.log(REC_MAX / REC_MIN)) * 1000);
const fmtWin = (m) => (m < 60 ? `${m}m` : m % 60 ? `${Math.floor(m / 60)}h${m % 60}m` : `${m / 60}h`);
const hhmm = (t) => { const d = new Date(t); return `${d.getHours()}:${String(d.getMinutes()).padStart(2, "0")}`; };
const DAY = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];

// Is any beat of a node inside [lo, hi]? Returns the latest activity in it, or 0.
function lastIn(beats, lo, hi, now) {
  let last = 0;
  for (const [s, e] of beats || []) if (s <= hi && (e || now) >= lo) last = Math.max(last, Math.min(e || now, hi));
  return last;
}

// The Claude projects dir of a folder: every non-alphanumeric char becomes "-";
// a subfolder belongs to the longest known projects dir that prefixes it.
function projectDir(folder, dirs) {
  if (!folder) return null;
  const enc = folder.replace(/[^A-Za-z0-9]/g, "-");
  let best = null;
  for (const d of dirs) if ((enc === d || enc.startsWith(d + "-")) && (!best || d.length > best.length)) best = d;
  return best || enc;
}

// Big contexts tint their row (the red bands are drawn above the tint):
// over 200k light yellow, over 300k light purple.
function ctxTint(ctx) {
  const dark = !!settings()?.dark;
  if (ctx > 300000) return dark ? "#3a2d55" : "#ece2fb";
  if (ctx > 200000) return dark ? "#3d3820" : "#fbf3c6";
  return null;
}

// The row look shared by sessions, subagents and calls: selected = bold 2px dark-blue frame,
// hover = light 1px frame (1px more padding keeps the size); every row reserves the 2px
// border so neither shifts it, and neither changes the background.
const SEL_BORDER = "#1e3a8a", HOVER_BORDER = "#1e3a8a66";
function framed(el, selected, pl, pr) {
  return el.pl(pl).pr(pr).py(0).border_2().border_color(selected ? SEL_BORDER : "#00000000")
    .when(!selected, (s) => s.hover((h) => h.border_1().border_color(HOVER_BORDER).pl(pl + 1).pr(pr + 1).py(1)));
}

// What scan.js reads the machine through (it runs unchanged under node for tests).
const io = () => ({
  platform: process.platform, home, exec, readTextFile,
  readdir: (p) => fs.readdir(p, { withFileTypes: true }),
  exists: (p) => fs.exists(p),
  readFile: (p) => fs.readFile(p),
  decode: (b) => Buffer.from(b).toString("utf8"),
});

function load(key = "state") {
  try { return JSON.parse(localStorage.getItem(key) || "{}") || {}; } catch { return {}; }
}

export default class AiSessions extends View {
  init(_props, cx) {
    this.scanner = new Scanner(io());
    this.sessions = [];
    this.dirs = [];
    this.error = "";
    const st = load();
    this.win = Math.min(REC_MAX, Math.max(REC_MIN, Number(st.win) || 15));
    this.pinned = st.pinned !== false;
    this.to = Number(st.to) || Date.now();
    this.only = !!st.only;
    this.closedProj = load("projects");  // project dir -> true when its group is folded
    // The selected row ({key, path, title, pick}) and the tree/calls split (tree's share).
    this.sel = load("sel").key ? load("sel") : null;
    this.split = Math.min(0.85, Math.max(0.15, Number(st.split) || 0.55));
    this.calls = [];
    this.callSel = {};  // selected session key -> send time of its picked call
    this.slider = SliderState.new({ min: 0, max: 1000, step: 1, value: recSliderPos(this.win) });
    this.slider.on("change", (v, cx) => { this.win = recWinAt(Number(v)); this.clamp(); cx.notify(); });
    this.slider.on("release", () => this.save());
    cx.timer.every(5000, (cx) => { this.refresh(cx); this.loadCalls(cx); });
    this.refresh(cx);
    this.loadCalls(cx);
    // Key bindings: "ai-sessions.full" = the Full view button, "ai-sessions.calls"
    // toggles the calls of the top session.
    cx.spawn(async (cx) => {
      for (;;) {
        const c = await nextCommand();
        if (c === "full") await this.full();
        else if (c === "calls" && this.first) this.select(this.first, cx);
      }
    });
  }

  async full() {
    try {
      await fullView(this.sel?.pick, (body) => notify({ title: "Full view", body, level: "info" }));
    } catch (e) {
      notify({ title: "Full view", body: String(e?.message || e), level: "error" });
    }
  }

  save() {
    try { localStorage.setItem("state", JSON.stringify({ win: this.win, pinned: this.pinned, to: this.to, only: this.only, split: this.split })); } catch {}
  }

  // Clicking a row selects it (its calls show below); clicking it again deselects.
  select(sel, cx) {
    this.sel = this.sel?.key === sel.key ? null : sel;
    this.calls = [];
    this.callsError = "";
    try { localStorage.setItem("sel", JSON.stringify(this.sel || {})); } catch {}
    this.loadCalls(cx);
    cx.notify();
  }

  // The selected transcript's API calls, refreshed with the 5 s scan only while selected.
  loadCalls(cx) {
    const sel = this.sel;
    if (!sel?.path) return;
    cx.spawn(async (cx) => {
      try {
        const calls = await this.scanner.calls(sel.path);
        if (this.sel?.key !== sel.key) return;
        this.calls = calls;
        this.callsError = "";
      } catch (e) {
        this.callsError = String(e);
      }
      cx.notify();
    });
  }

  span(now) {
    const to = this.pinned ? now : this.to;
    return [to - this.win * MIN, to];
  }

  // Keeps an unpinned span inside the 3 days; back at the right edge it re-pins to now.
  clamp() {
    if (this.pinned) return;
    const now = Date.now();
    this.to = Math.max(now - TL + this.win * MIN, Math.min(now, this.to));
    if (now - this.to < TL * 0.01) this.pinned = true;
  }

  refresh(cx) {
    cx.spawn(async (cx) => {
      try {
        const t0 = Date.now();
        if (this.scanning) return;  // a slow first scan must not overlap the next tick
        this.scanning = true;
        const d = await this.scanner.scan(DAYS).finally(() => { this.scanning = false; });
        this.sessions = d.sessions;
        this.dirs = d.dirs;
        this.scanMs = Date.now() - t0;
        this.error = "";
      } catch (e) {
        this.error = String(e);
      }
      this.clamp();
      cx.notify();
    });
  }

  // Timeline drag: the pointer's time is where the grabbed point of the span goes.
  dragTo(e, cx) {
    if (!e.bounds || !e.local_position) return;
    const now = Date.now(), t = now - TL + (e.local_position.x / e.bounds.width) * TL;
    this.pinned = false;
    this.to = t + this.grab;
    this.clamp();
    cx.notify();
  }

  timeline(t, now, lo, hi) {
    const start = now - TL, x = (v) => `${Math.max(0, Math.min(100, ((v - start) / TL) * 100)).toFixed(2)}%`;
    // Activity density: API calls per 30-min bucket, sessions and subagents together.
    const N = 144, bucket = TL / N, counts = new Array(N).fill(0);
    for (const s of this.sessions) for (const n of [s, ...s.subagents]) for (const [b] of n.beats) {
      const i = Math.floor((b - start) / bucket);
      if (i >= 0 && i < N) counts[i]++;
    }
    const max = Math.max(1, ...counts), bars = PathBuilder.fill();
    counts.forEach((c, i) => {
      if (!c) return;
      const h = 100 - Math.max(8, Math.sqrt(c / max) * 100);
      bars.add_polygon([[x(start + i * bucket), `${h}%`], [x(start + (i + 1) * bucket), `${h}%`],
        [x(start + (i + 1) * bucket), "100%"], [x(start + i * bucket), "100%"]], true);
    });
    const days = PathBuilder.stroke(1), labels = [];
    const d = new Date(now); d.setHours(0, 0, 0, 0);
    for (let m = d.getTime(); m > start; m -= 86400000) {
      days.move_to(x(m), "0%").line_to(x(m), "100%");
      labels.push(div().absolute().top(0).left(x(m)).pl(3).text_size(10).text_color(t.dim).child(DAY[new Date(m).getDay()]));
    }
    const sel = PathBuilder.fill().add_polygon([[x(lo), "0%"], [x(hi), "0%"], [x(hi), "100%"], [x(lo), "100%"]], true);
    const edge = PathBuilder.stroke(1.5).add_polygon([[x(lo), "0%"], [x(hi), "0%"], [x(hi), "100%"], [x(lo), "100%"]], true);
    const layer = (p, c) => window.paint_path(p.build(), c).absolute().top(0).left(0).size_full();
    return div().id("timeline").relative().h(30).mx(8).rounded(3).bg(t.surface).overflow_hidden().cursor_pointer()
      .child(layer(days, Background.solid(t.border)))
      .children(labels)
      .child(layer(bars, Background.solid(EKG).opacity(0.45)))
      .child(layer(sel, Background.solid(t.accent).opacity(0.18)))
      .child(layer(edge, Background.solid(t.accent).opacity(0.8)))
      .on_mouse_down("left", (e, cx) => {
        if (!e.bounds || !e.local_position) return;
        const pt = now - TL + (e.local_position.x / e.bounds.width) * TL, [a, b] = this.span(Date.now());
        this.grab = pt >= a && pt <= b ? b - pt : (b - a) / 2;  // grab inside keeps the offset, outside centres
        this.drag = true;
        this.dragTo(e, cx);
      })
      .on_mouse_move((e, cx) => { if (this.drag) this.dragTo(e, cx); })
      .on_mouse_up("left", () => { this.drag = false; this.save(); })
      .on_hover((h) => { if (!h && this.drag) { this.drag = false; this.save(); } });
  }

  controls(t, now, lo, hi, proj) {
    const box = div().size(12).flex_none().rounded(2).border_1().border_color(t.input).items_center().justify_center()
      .when(this.only, (s) => s.bg(t.accent).border_color(t.accent));
    return v_flex().gap(4).pb(4)
      .child(h_flex().px(8).pt(8).items_center().gap(6)
        .child(div().flex_1().text_size(11).text_color(t.muted).child("AI sessions"))
        .child(div().id("full").px(5).rounded(3).border_1().border_color(t.border).cursor_pointer()
          .text_size(11).text_color(t.foreground).hover((s) => s.bg(t.hover))
          .on_click((_e, cx) => cx.spawn(() => this.full()))
          .child("Full view ↗"))
        .child(h_flex().id("only").gap(5).items_center().cursor_pointer().text_size(11).text_color(t.foreground)
          .on_click((_e, cx) => { this.only = !this.only; this.save(); cx.notify(); })
          .child(box).child("Only this project")))
      .child(h_flex().px(8).gap(8).items_center()
        .child(div().text_size(11).text_color(t.muted).child("Window"))
        .child(div().flex_1().child(Slider.new(this.slider).child(
          SliderTrack.new(this.slider).flex().items_center().h(18).w_full().child(
            SliderIndicator.new(this.slider).relative().w_full().h(4).rounded(2).bg(t.border)
              .range_style((f) => f.rounded(2).bg(t.accent))
              .child(SliderThumb.new(this.slider).id("thumb").size(12).rounded(6).bg(t.accent).ml(-6))))))
        .child(div().w(44).flex_none().text_right().font_semibold().text_color(t.foreground).child(fmtWin(this.win))))
      .child(this.timeline(t, now, lo, hi))
      .child(h_flex().px(8).text_size(11).text_color(t.muted)
        .child(div().flex_1().truncate().child(`${hhmm(lo)} – ${this.pinned ? "now" : hhmm(hi)}${proj ? ` · ${proj}` : ""}`))
        .child(div().child(this.scanMs != null ? `scan ${this.scanMs}ms` : "")));
  }

  // One line: mark · [icon] · title (truncated) · compact meta · context on the right.
  row(id, depth, t, mark, icon, title, meta, ctx, beats, span, onClick, selected) {
    const pulse = ekg(beats, ...span), tint = ctxTint(ctx);
    return framed(h_flex().id(id).relative().gap(6).rounded(4).items_center(), selected, 18 + depth * 28, 6)
      .when(!!tint, (s) => s.bg(tint))
      .when(!!pulse, (s) => s.child(pulse))
      .when(!!onClick, (s) => s.on_click(onClick))
      .child(mark)
      .when(!!icon, (s) => s.child(icon))
      .child(div().flex_1().min_w(0).truncate().line_height("18px").text_color(t.foreground).when(depth === 0, (s) => s.font_semibold()).child(title))
      .when(!!meta, (s) => s.child(div().flex_none().text_size(11).text_color(t.muted).child(meta)))
      .child(div().w(34).flex_none().text_right().font_semibold().text_color(t.foreground).child(k(ctx)));
  }

  // A chevron that alone folds/unfolds (the click doesn't reach its row).
  chevron(id, open, t, color, onToggle) {
    return div().id(id).w(12).flex_none().text_color(color).cursor_pointer().hover((s) => s.text_color(t.foreground))
      .on_click((_e, cx) => { cx.stop_propagation(); onToggle(); cx.notify(); })
      .child(open ? "▾" : "▸");
  }

  // A project group's header: compact muted caps over a thin separator; its chevron folds it.
  header(key, g, open, t) {
    return h_flex().id(`p-${key}`).gap(6).px(8).pt(6).pb(2).mx(4).items_center()
      .border_b_1().border_color(t.border).text_size(10).text_color(t.muted)
      .child(this.chevron(`pc-${key}`, open, t, t.muted, () => {
        if (open) this.closedProj[key] = true; else delete this.closedProj[key];
        try { localStorage.setItem("projects", JSON.stringify(this.closedProj)); } catch {}
      }))
      .child(div().flex_1().min_w(0).truncate().font_semibold().child(String(g.name || "?").toUpperCase()))
      .child(div().flex_none().child(`${g.items.length}`));
  }

  render() {
    const t = theme(), now = Date.now(), [lo, hi] = this.span(now), span = [lo, hi, now];
    this.collapsed ??= {};
    let dir = null;
    if (this.only) {
      const f = focused();
      dir = projectDir(f?.cwd || spaces().find((s) => s.id === f?.space)?.folder, this.dirs);
    }
    const shown = [];
    for (const s of this.sessions) {
      if (dir && s.dir !== dir) continue;
      const subs = s.subagents.map((a) => [a, lastIn(a.beats, lo, hi, now)]).filter(([, l]) => l);
      const last = Math.max(lastIn(s.beats, lo, hi, now), ...subs.map(([, l]) => l));
      subs.sort((x, y) => y[1] - x[1] || y[0].mtime - x[0].mtime);  // newest subagent first
      if (last) shown.push([last, s, subs.map(([a]) => a)]);
    }
    shown.sort((a, b) => b[0] - a[0]);
    // Group by project, groups ordered by their newest session (the first one seen).
    const groups = new Map();
    for (const e of shown.slice(0, 50)) {
      const key = e[1].dir || e[1].project;
      if (!groups.has(key)) groups.set(key, { name: e[1].project, mtime: 0, items: [] });
      const g = groups.get(key);
      g.items.push(e);
      g.mtime = Math.max(g.mtime, e[1].mtime);
    }
    const nodes = [];
    let first = null;
    for (const [key, g] of groups) {
      const gopen = !this.closedProj[key];
      nodes.push(this.header(key, g, gopen, t));
      if (!gopen) continue;
      for (const [, s, subs] of g.items) {
        const open = !this.collapsed[s.id], n = subs.length;
        const pick = { id: s.id, provider: s.provider }, key = `s-${s.id}`;
        first ??= { key, path: s.path, title: s.title, pick };
        nodes.push(this.row(key, 0, t,
          n ? this.chevron(`c-${s.id}`, open, t, t.muted, () => { this.collapsed[s.id] = open; }) : div().w(12).flex_none(),
          image(`icons/${s.provider === "codex" ? "codex" : "claude"}.svg`).w(13).h(13).flex_none(),
          s.title, "", s.ctx, s.beats, span,
          (_e, cx) => this.select({ key, path: s.path, title: s.title, pick }, cx), this.sel?.key === key));
        if (open) for (const a of subs) {
          const akey = `a-${a.id}`, atitle = a.description || a.id;
          const apick = { id: s.provider === "codex" ? a.id : `agent-${a.id}`, provider: s.provider };
          nodes.push(this.row(akey, 1, t,
            div().w(12).flex_none().text_color(t.dim).child("└"), null,
            atitle, [a.type && a.type !== "general-purpose" ? a.type : "", a.finished ? "" : "running"].filter(Boolean).join(" · "), a.ctx, a.beats, span,
            (_e, cx) => this.select({ key: akey, path: a.path, title: atitle, pick: apick }, cx), this.sel?.key === akey));
        }
      }
    }
    this.first = first;
    const proj = dir ? (dir.startsWith("-") ? dir.split("-").pop() : dir) : "";
    return v_flex().id("root").size_full().bg(t.chrome).text_size(13).p(4)
      .child(this.controls(t, now, lo, hi, proj))
      .when(!!this.error, (s) => s.child(div().px(8).text_color(t.blocked).child(this.error)))
      .when(!nodes.length && !this.error, (s) => s.child(div().px(8).py(6).text_size(12).text_color(t.muted).child("No sessions in this span")))
      .child(v_flex().id("body").flex_1().min_h(0)
        .on_size_change((size, cx) => { this.bodyH = size.height; cx.notify(); })
        .on_mouse_move((e, cx) => {
          if (!this.dragSplit || !e.bounds || !e.local_position) return;
          this.split = Math.min(0.85, Math.max(0.15, e.local_position.y / e.bounds.height));
          cx.notify();
        })
        .on_mouse_up("left", () => { if (this.dragSplit) { this.dragSplit = false; this.save(); } })
        .child(v_flex().id("tree").when(!this.sel, (s) => s.flex_1()).when(!!this.sel, (s) => s.flex_none().h((this.bodyH || 400) * this.split))
          .overflow_y_scrollbar().children(nodes))
        .when(!!this.sel, (s) => s
          .child(div().id("split").h(7).flex_none().cursor_row_resize().flex().items_center()
            .on_mouse_down("left", (_e, cx) => { this.dragSplit = true; cx.notify(); })
            .child(div().h(1).w_full().bg(t.border)))
          .child(this.callsPane(t, now))));
  }

  // The selected transcript's calls, newest first: time · trigger · what it did · duration ·
  // context · its growth over the previous call (token-monitor's callDeltaAt).
  callsPane(t, now) {
    const sec = (ms) => (ms < 60000 ? `${(ms / 1000).toFixed(1)}s` : `${Math.floor(ms / 60000)}m${String(Math.round(ms / 1000) % 60).padStart(2, "0")}`);
    const hms = (ms) => new Date(ms).toTimeString().slice(0, 8);
    const did = (c) => {
      if (!c.end) return "in flight…";
      if (!c.tools.length) return c.text || "(thinking)";
      const n = {};
      for (const x of c.tools) n[x] = (n[x] || 0) + 1;
      return Object.entries(n).map(([x, m]) => (m > 1 ? `${x} ×${m}` : x)).join(", ");
    };
    // A call is picked by its send time; the pick is kept per selected session (in memory).
    const key = this.sel?.key, picked = this.callSel[key];
    const rows = this.calls.map((c, i) => {
      const on = c.sent === picked, tint = c.end ? ctxTint(c.ctx) : null;
      return framed(h_flex().id(`call-${i}`).gap(6).rounded(4).items_center().text_size(11), on, 6, 6)
      .when(!c.end, (s) => s.bg(t.hover))
      .when(!!tint, (s) => s.bg(tint))
      .on_click((_e, cx) => { if (on) delete this.callSel[key]; else this.callSel[key] = c.sent; cx.notify(); })
      .child(div().w(50).flex_none().text_color(t.muted).child(hms(c.sent)))
      .child(div().w(8).flex_none().text_color(c.by === "prompt" ? t.accent : t.dim).child(c.by === "prompt" ? "›" : "·"))
      .child(div().flex_1().min_w(0).truncate().text_color(c.end ? t.foreground : t.working).child(did(c)))
      .child(div().w(38).flex_none().text_right().text_color(t.muted).child(sec((c.end || now) - c.sent)))
      .child(div().w(34).flex_none().text_right().font_semibold().text_color(t.foreground).child(c.end ? k(c.ctx) : ""))
      .child(div().w(38).flex_none().text_right().text_color(c.delta > 20000 ? t.blocked : t.muted).child(c.end ? `${c.delta >= 0 ? "+" : "−"}${k(Math.abs(c.delta))}` : ""));
    });
    return v_flex().id("calls").flex_1().min_h(0)
      .child(h_flex().px(8).pb(2).gap(6).text_size(10).text_color(t.muted)
        .child(div().flex_1().min_w(0).truncate().font_semibold().child(`CALLS · ${this.sel.title}`))
        .child(div().flex_none().child(`${this.calls.filter((c) => c.end).length}`)))
      .when(!!this.callsError, (s) => s.child(div().px(8).text_size(11).text_color(t.blocked).child(this.callsError)))
      .when(!rows.length && !this.callsError, (s) => s.child(div().px(8).py(4).text_size(11).text_color(t.muted).child("No calls")))
      .child(v_flex().id("calllist").flex_1().overflow_y_scrollbar().children(rows));
  }
}
