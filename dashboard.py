#!/usr/bin/env python3
"""Read-only web dashboard for opencode session token usage, shown as a subagent tree."""

import html
import json
import os
import sqlite3
import sys
import threading
import time
import urllib.parse
import webbrowser
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

VERSION = "2.1.0"

PORT = int(os.environ.get("PORT", "8787"))
PORT_TRIES = 20  # on "address already in use", try PORT, PORT+1, … up to this many ports
_main_srv = None  # the running server, for tests to shut down

ACTIVE_WINDOW_MS = 15 * 60 * 1000  # a session updated within this window is "active"

DB_PATH = ""        # active DB path; resolved at startup, switchable at runtime via POST /api/db
DB_GEN = 0          # bumped on every switch; per-thread connections reconnect on mismatch
_db_lock = threading.Lock()
db_local = threading.local()


def validate_db(path):
    """(ok, error) — the path exists, opens read-only, and has the `session` table."""
    if not path:
        return False, "empty path"
    if not os.path.exists(path):
        return False, f"file not found: {path}"
    uri = "file:" + urllib.parse.quote(path) + "?mode=ro"
    try:
        conn = sqlite3.connect(uri, uri=True)
        try:
            conn.execute("SELECT 1 FROM session LIMIT 1")
        finally:
            conn.close()
    except sqlite3.Error as e:
        return False, str(e)
    return True, None


def parse_db_flag(argv=None):
    """The --db PATH / --db=PATH CLI override; None if absent."""
    argv = sys.argv if argv is None else argv
    for i, a in enumerate(argv):
        if a == "--db" and i + 1 < len(argv):
            return argv[i + 1]
        if a.startswith("--db="):
            return a[len("--db="):]
    return None


def db_candidates(cli_path=None, env_path=None):
    """Candidate DB paths in priority order: --db flag, DB_PATH env, then platform defaults."""
    out = []
    if cli_path:
        out.append(os.path.expanduser(cli_path))
    env_path = os.environ.get("DB_PATH") if env_path is None else env_path
    if env_path:
        out.append(os.path.expanduser(env_path))
    home = os.path.expanduser("~")
    xdg = os.environ.get("XDG_DATA_HOME") or os.path.join(home, ".local", "share")
    out.append(os.path.join(xdg, "opencode", "opencode.db"))
    if sys.platform == "darwin":
        out.append(os.path.join(home, "Library", "Application Support", "opencode", "opencode.db"))
    if sys.platform == "win32":
        for var in ("APPDATA", "LOCALAPPDATA"):
            base = os.environ.get(var)
            if base:
                out.append(os.path.join(base, "opencode", "opencode.db"))
    seen, uniq = set(), []
    for p in out:
        if p not in seen:
            seen.add(p)
            uniq.append(p)
    return uniq


def resolve_db(cli_path=None):
    """First valid candidate wins; else the first candidate with found=False."""
    cands = db_candidates(cli_path)
    for p in cands:
        ok, _ = validate_db(p)
        if ok:
            return p, True
    return (cands[0] if cands else ""), False


def set_db(path):
    """Validate and switch the active DB. Returns (ok, error)."""
    global DB_PATH, DB_GEN
    with _db_lock:
        ok, err = validate_db(path)
        if ok:
            DB_PATH = path
            DB_GEN += 1
        return ok, err


def db():
    conn = getattr(db_local, "conn", None)
    if conn is None or getattr(db_local, "gen", None) != DB_GEN:
        if conn is not None:
            conn.close()
        uri = "file:" + urllib.parse.quote(DB_PATH) + "?mode=ro"
        conn = sqlite3.connect(uri, uri=True)
        conn.row_factory = sqlite3.Row
        db_local.conn = conn
        db_local.gen = DB_GEN
    return conn


def fmt_duration(ms):
    if not ms or ms < 0:
        return "—"
    s = ms / 1000
    if s < 60:
        return f"{s:.0f}s"
    m = s / 60
    if m < 60:
        return f"{m:.1f}m"
    return f"{m / 60:.1f}h"


def session_max_contexts():
    """Per-session max context size (fresh input + cache read + write) across that session's own AI calls."""
    out = {}
    rows = db().execute(
        """SELECT m.session_id AS sid, m.data FROM message m
           JOIN session s ON s.id = m.session_id
           WHERE s.time_archived IS NULL AND s.directory != ''
             AND m.data LIKE '%"role":"assistant"%'"""
    ).fetchall()
    for r in rows:
        try:
            d = json.loads(r["data"])
        except (json.JSONDecodeError, TypeError):
            continue
        if d.get("role") != "assistant":
            continue
        t = d.get("tokens") or {}
        cache = t.get("cache") or {}
        ctx = (t.get("input") or 0) + (cache.get("read") or 0) + (cache.get("write") or 0)
        if ctx and ctx > out.get(r["sid"], 0):
            out[r["sid"]] = ctx
    return out


def build_tree():
    maxctx = session_max_contexts()
    now_ms = int(time.time() * 1000)
    rows = db().execute(
        """SELECT id, parent_id, title, agent, model, directory,
                  tokens_input, tokens_output, tokens_reasoning,
                  tokens_cache_read, tokens_cache_write,
                  time_created, time_updated
           FROM session WHERE time_archived IS NULL AND directory != ''"""
    ).fetchall()

    nodes = {}
    for r in rows:
        model = r["model"]
        if model:
            try:
                model = json.loads(model)
            except (json.JSONDecodeError, TypeError):
                pass
        updated = r["time_updated"] or 0
        nodes[r["id"]] = {
            "id": r["id"],
            "parent": r["parent_id"],
            "title": r["title"] or "(untitled)",
            "agent": r["agent"],
            "model": model,
            "dir": r["directory"],
            "maxctx": maxctx.get(r["id"], 0),
            "in": r["tokens_input"],
            "out": r["tokens_output"],
            "reason": r["tokens_reasoning"],
            "cr": r["tokens_cache_read"],
            "cw": r["tokens_cache_write"],
            "created": r["time_created"],
            "updated": updated,
            "active": updated > 0 and now_ms - updated < ACTIVE_WINDOW_MS,
            "dur": max(0, (r["time_updated"] or 0) - (r["time_created"] or 0)),
            "children": [],
        }

    roots = []
    for n in nodes.values():
        p = n["parent"]
        if p in nodes:
            nodes[p]["children"].append(n)
        else:
            roots.append(n)

    for n in nodes.values():
        n["beats"] = session_beats(n["id"], now_ms - ACTIVE_WINDOW_MS) if n["active"] else []

    def subtree(n):
        t = {
            "in": n["in"], "out": n["out"], "reason": n["reason"],
            "cr": n["cr"], "cw": n["cw"],
        }
        for c in n["children"]:
            st = subtree(c)
            for k in t:
                t[k] += st[k]
        n["subtree"] = t
        n["children"].sort(key=lambda c: c["created"] or 0, reverse=True)
        return t

    for r in roots:
        subtree(r)
    roots.sort(key=lambda r: r["created"] or 0, reverse=True)

    return roots


def session_beats(session_id, since_ms):
    """Timestamps (ms) of the session's own AI calls since since_ms — the "heartbeat" of the sparkline."""
    rows = db().execute(
        """SELECT time_created, data FROM message
           WHERE session_id = ? AND time_created >= ? AND data LIKE '%"role":"assistant"%'""",
        (session_id, since_ms),
    ).fetchall()
    beats = []
    for r in rows:
        try:
            d = json.loads(r["data"])
        except (json.JSONDecodeError, TypeError):
            continue
        if d.get("role") != "assistant":
            continue
        t = d.get("tokens") or {}
        cache = t.get("cache") or {}
        if (t.get("input") or 0) or (t.get("output") or 0) or (t.get("reasoning") or 0) \
                or (cache.get("read") or 0) or (cache.get("write") or 0):
            beats.append(r["time_created"])
    return sorted(beats)


def session_prompt(session_id):
    """First real user text parts of the session = the request sent to that (sub)agent."""
    rows = db().execute(
        """SELECT p.data FROM part p
           JOIN message m ON m.id = p.message_id
           WHERE p.session_id = ? AND m.data LIKE '%"role":"user"%'
           ORDER BY m.time_created, p.id""",
        (session_id,),
    ).fetchall()
    texts = []
    for r in rows:
        try:
            d = json.loads(r["data"])
        except json.JSONDecodeError:
            continue
        if d.get("type") == "text" and d.get("text") and not d.get("synthetic"):
            t = d["text"].strip()
            if t and not t.startswith("<system-reminder>"):
                texts.append(t)
                if sum(len(x) for x in texts) > 20000:
                    break
    return texts


TRIGGER_MAX = 60000


def _clip(s):
    if s is None:
        return "", False
    if not isinstance(s, str):
        s = json.dumps(s, ensure_ascii=False, indent=1)
    if len(s) > TRIGGER_MAX:
        return s[:TRIGGER_MAX], True
    return s, False


def _human_message_ids(session_id):
    """Ids of user messages that contain real human text (not synthetic, not a system reminder)."""
    rows = db().execute(
        """SELECT p.message_id AS mid, p.data FROM part p
           JOIN message m ON m.id = p.message_id
           WHERE p.session_id = ? AND m.data LIKE '%"role":"user"%'""",
        (session_id,),
    ).fetchall()
    out = set()
    for r in rows:
        try:
            d = json.loads(r["data"])
        except (json.JSONDecodeError, TypeError):
            continue
        if d.get("type") == "text" and d.get("text") and not d.get("synthetic"):
            t = d["text"].strip()
            if t and not t.startswith("<system-reminder>"):
                out.add(r["mid"])
    return out


def session_calls(session_id):
    """Per-AI-call token usage: one record per assistant message that recorded usage.
    `hu` marks calls whose preceding message is a user message with real human text."""
    rows = db().execute(
        """SELECT id, time_created, data FROM message
           WHERE session_id = ?
           ORDER BY time_created, id""",
        (session_id,),
    ).fetchall()
    human_ids = _human_message_ids(session_id)
    calls = []
    prev = None
    for r in rows:
        try:
            d = json.loads(r["data"])
        except json.JSONDecodeError:
            d = {}
        if d.get("role") == "assistant":
            t = d.get("tokens") or {}
            cache = t.get("cache") or {}
            inp = t.get("input") or 0
            cr = cache.get("read") or 0
            cw = cache.get("write") or 0
            out = t.get("output") or 0
            reason = t.get("reasoning") or 0
            if inp or cr or cw or out or reason:
                calls.append({
                    "mid": r["id"],
                    "time": r["time_created"],
                    "in": inp, "cr": cr, "cw": cw,
                    "out": out, "reason": reason,
                    "ctx": inp + cr + cw,
                    "hu": bool(prev) and prev["id"] in human_ids,
                })
        prev = r
    return calls


def call_trigger(message_id):
    """The input that triggered a given assistant call: content of the preceding message."""
    row = db().execute(
        "SELECT id, session_id, time_created, data FROM message WHERE id = ?",
        (message_id,),
    ).fetchone()
    if not row:
        return None
    try:
        d = json.loads(row["data"])
    except json.JSONDecodeError:
        d = {}
    if d.get("role") != "assistant":
        return None
    prev = db().execute(
        """SELECT id, time_created, data FROM message
           WHERE session_id = ? AND (time_created < ? OR (time_created = ? AND id < ?))
           ORDER BY time_created DESC, id DESC LIMIT 1""",
        (row["session_id"], row["time_created"], row["time_created"], row["id"]),
    ).fetchone()
    if not prev:
        return {"case": "start", "time": None, "parts": []}
    try:
        pd = json.loads(prev["data"])
    except json.JSONDecodeError:
        pd = {}
    parts = []
    prows = db().execute(
        "SELECT data FROM part WHERE message_id = ? ORDER BY id", (prev["id"],)
    ).fetchall()
    for p in prows:
        try:
            pj = json.loads(p["data"])
        except json.JSONDecodeError:
            continue
        t = pj.get("type")
        if t == "text" and pj.get("text"):
            txt, cut = _clip(pj["text"])
            parts.append({"kind": "text", "text": txt, "cut": cut})
        elif t == "tool":
            st = pj.get("state") or {}
            inp_s, cut_i = _clip(st.get("input"))
            out_s, cut_o = _clip(st.get("output"))
            parts.append({"kind": "tool", "tool": pj.get("tool"), "status": st.get("status"),
                          "input": inp_s, "cut_i": cut_i, "output": out_s, "cut_o": cut_o})
    if pd.get("role") == "user":
        return {"case": "user", "time": prev["time_created"], "parts": parts}
    return {"case": "assistant", "time": prev["time_created"], "parts": parts}


def page_file():
    if getattr(sys, "frozen", False):  # PyInstaller bundle: index.html sits in _MEIPASS
        return os.path.join(sys._MEIPASS, "index.html")
    return os.path.join(os.path.dirname(os.path.abspath(__file__)), "src", "index.html")


def load_page():
    # read per request: edit src/index.html -> refresh browser, no restart needed
    with open(page_file(), encoding="utf-8") as f:
        return f.read().replace("{{VERSION}}", VERSION)


def i18n_file():
    if getattr(sys, "frozen", False):  # PyInstaller bundle: i18n.js sits in _MEIPASS
        return os.path.join(sys._MEIPASS, "i18n.js")
    return os.path.join(os.path.dirname(os.path.abspath(__file__)), "src", "i18n.js")


def load_i18n():
    # read per request, same edit-and-refresh flow as the page
    with open(i18n_file(), encoding="utf-8") as f:
        return f.read()


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass

    def _json(self, obj, code=200):
        body = json.dumps(obj).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Cache-Control", "no-store")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        path = urllib.parse.urlparse(self.path).path
        try:
            if path == "/api/db":
                ok, err = validate_db(DB_PATH)
                self._json({"path": DB_PATH, "found": ok, "error": err,
                            "candidates": db_candidates(parse_db_flag())})
            elif not validate_db(DB_PATH)[0]:
                self._json({"error": "db_not_found", "path": DB_PATH}, 503)
            elif path == "/api/tree":
                self._json(build_tree())
            elif path.startswith("/api/prompt/"):
                sid = path[len("/api/prompt/"):]
                self._json({"texts": session_prompt(sid)})
            elif path.startswith("/api/calls/"):
                sid = path[len("/api/calls/"):]
                self._json({"calls": session_calls(sid)})
            elif path.startswith("/api/trigger/"):
                mid = path[len("/api/trigger/"):]
                res = call_trigger(mid)
                self._json(res if res is not None else {"error": "message not found"},
                            200 if res is not None else 404)
            elif path == "/i18n.js":
                self.send_response(200)
                self.send_header("Content-Type", "application/javascript; charset=utf-8")
                self.send_header("Cache-Control", "no-store")
                self.end_headers()
                self.wfile.write(load_i18n().encode())
            elif path == "/":
                self.send_response(200)
                self.send_header("Content-Type", "text/html; charset=utf-8")
                self.send_header("Cache-Control", "no-store")
                self.end_headers()
                self.wfile.write(load_page().encode())
            else:
                self.send_error(404)
        except sqlite3.Error as e:
            self._json({"error": str(e)}, 500)

    def do_POST(self):
        if urllib.parse.urlparse(self.path).path != "/api/db":
            self.send_error(404)
            return
        try:
            length = int(self.headers.get("Content-Length") or 0)
            body = json.loads(self.rfile.read(length) or b"{}")
        except (ValueError, json.JSONDecodeError):
            self._json({"ok": False, "error": "invalid JSON body"}, 400)
            return
        ok, err = set_db(body.get("path"))
        out = {"ok": ok, "path": body.get("path")}
        if err:
            out["error"] = err
        self._json(out, 200 if ok else 400)


def main():
    if "--version" in sys.argv:
        print(VERSION)
        return
    global DB_PATH, DB_GEN
    DB_PATH, found = resolve_db(cli_path=parse_db_flag())
    DB_GEN += 1
    if not found:
        print(f"DB not found: {DB_PATH} — set the path in the page header")
    global _main_srv
    srv = None
    for off in range(PORT_TRIES):
        p = PORT + off
        try:
            srv = ThreadingHTTPServer(("127.0.0.1", p), Handler)
            if off:
                print(f"Port {PORT} already in use — using {p} instead.")
            break
        except OSError as e:
            if e.errno != 98 or off == PORT_TRIES - 1:
                print(f"Cannot start server on port {p}: {e}")
                print(f"Try a different port:  PORT={p + 1} python3 dashboard.py")
                return
    url = f"http://localhost:{srv.server_address[1]}"
    _main_srv = srv
    print(f"Token usage tree: {url}  (DB: {DB_PATH}, read-only)")
    if not os.environ.get("NO_BROWSER"):
        threading.Timer(0.5, lambda: webbrowser.open(url)).start()
    try:
        srv.serve_forever()
    except KeyboardInterrupt:
        print("\nbye")


if __name__ == "__main__":
    main()
