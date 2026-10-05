#!/usr/bin/env python3
"""Read-only web dashboard for opencode session token usage, shown as a subagent tree."""

import html
import json
import os
import sqlite3
import threading
import time
import urllib.parse
import webbrowser
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

DB_PATH = os.path.expanduser("~/.local/share/opencode/opencode.db")
PORT = int(os.environ.get("PORT", "8787"))

db_local = threading.local()


def db():
    conn = getattr(db_local, "conn", None)
    if conn is None:
        uri = "file:" + urllib.parse.quote(DB_PATH) + "?mode=ro"
        conn = sqlite3.connect(uri, uri=True)
        conn.row_factory = sqlite3.Row
        db_local.conn = conn
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
            "updated": r["time_updated"],
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


def session_calls(session_id):
    """Per-AI-call token usage: one record per assistant message that recorded usage."""
    rows = db().execute(
        """SELECT id, time_created, data FROM message
           WHERE session_id = ? AND data LIKE '%"role":"assistant"%'
           ORDER BY time_created, id""",
        (session_id,),
    ).fetchall()
    calls = []
    for r in rows:
        try:
            d = json.loads(r["data"])
        except json.JSONDecodeError:
            continue
        if d.get("role") != "assistant":
            continue
        t = d.get("tokens") or {}
        cache = t.get("cache") or {}
        inp = t.get("input") or 0
        cr = cache.get("read") or 0
        cw = cache.get("write") or 0
        out = t.get("output") or 0
        reason = t.get("reasoning") or 0
        if not (inp or cr or cw or out or reason):
            continue
        calls.append({
            "mid": r["id"],
            "time": r["time_created"],
            "in": inp, "cr": cr, "cw": cw,
            "out": out, "reason": reason,
            "ctx": inp + cr + cw,
        })
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


PAGE_FILE = os.path.join(os.path.dirname(os.path.abspath(__file__)), "index.html")


def load_page():
    # read per request: edit index.html -> refresh browser, no restart needed
    with open(PAGE_FILE, encoding="utf-8") as f:
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
            if path == "/api/tree":
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


def main():
    if not os.path.exists(DB_PATH):
        print(f"DB not found: {DB_PATH}")
        return
    # smoke check read-only access
    try:
        db().execute("SELECT 1 FROM session LIMIT 1").fetchone()
    except sqlite3.Error as e:
        print(f"Cannot read DB: {e}")
        return
    url = f"http://localhost:{PORT}"
    srv = ThreadingHTTPServer(("127.0.0.1", PORT), Handler)
    print(f"Token usage tree: {url}  (DB: {DB_PATH}, read-only)")
    if not os.environ.get("NO_BROWSER"):
        threading.Timer(0.5, lambda: webbrowser.open(url)).start()
    try:
        srv.serve_forever()
    except KeyboardInterrupt:
        print("\nbye")


if __name__ == "__main__":
    main()
