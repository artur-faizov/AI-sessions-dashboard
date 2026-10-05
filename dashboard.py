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


def build_tree():
    rows = db().execute(
        """SELECT id, parent_id, title, agent, model, directory, cost,
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
                m = json.loads(model)
                model = m.get("modelID", model)
            except (json.JSONDecodeError, TypeError):
                pass
        nodes[r["id"]] = {
            "id": r["id"],
            "parent": r["parent_id"],
            "title": r["title"] or "(untitled)",
            "agent": r["agent"],
            "model": model,
            "dir": r["directory"],
            "cost": r["cost"],
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
            "cr": n["cr"], "cw": n["cw"], "cost": n["cost"],
        }
        for c in n["children"]:
            st = subtree(c)
            for k in t:
                t[k] += st[k]
        n["subtree"] = t
        n["children"].sort(key=lambda c: c["created"] or 0)
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
            elif path == "/":
                self.send_response(200)
                self.send_header("Content-Type", "text/html; charset=utf-8")
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
    threading.Timer(0.5, lambda: webbrowser.open(url)).start()
    try:
        srv.serve_forever()
    except KeyboardInterrupt:
        print("\nbye")


if __name__ == "__main__":
    main()
