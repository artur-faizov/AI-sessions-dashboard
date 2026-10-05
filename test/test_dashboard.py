"""Tests for dashboard.py — run: python3 test/test_dashboard.py"""

import contextlib
import io
import json
import os
import sqlite3
import socketserver
import sys
import tempfile
import threading
import time
import unittest
import urllib.error
import urllib.request
from http.server import ThreadingHTTPServer

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
import dashboard  # noqa: E402

SCHEMA = """
CREATE TABLE message (
  id text PRIMARY KEY,
  session_id text NOT NULL,
  time_created integer NOT NULL,
  time_updated integer NOT NULL,
  data text NOT NULL
);
CREATE TABLE part (
  id text PRIMARY KEY,
  message_id text NOT NULL,
  session_id text NOT NULL,
  time_created integer NOT NULL,
  time_updated integer NOT NULL,
  data text NOT NULL
);
CREATE TABLE session (
  id text PRIMARY KEY,
  parent_id text,
  title text,
  agent text,
  model text,
  directory text NOT NULL,
  tokens_input integer DEFAULT 0 NOT NULL,
  tokens_output integer DEFAULT 0 NOT NULL,
  tokens_reasoning integer DEFAULT 0 NOT NULL,
  tokens_cache_read integer DEFAULT 0 NOT NULL,
  tokens_cache_write integer DEFAULT 0 NOT NULL,
  time_created integer NOT NULL,
  time_updated integer NOT NULL,
  time_archived integer
);
"""


def jdump(o):
    return json.dumps(o, separators=(",", ":"))


def user(mid, t):
    return (mid, "s1", t, t, jdump({"role": "user", "time": {"created": t}}))


def assistant(mid, t, inp=10, out=5):
    return (mid, "s1", t, t, jdump({
        "role": "assistant",
        "tokens": {"input": inp, "output": out,
                   "cache": {"read": 0, "write": 0}, "reasoning": 0},
    }))


def part(pid, mid, t, data):
    return (pid, mid, "s1", t, t, jdump(data))


class BuildTreeActiveTest(unittest.TestCase):
    def setUp(self):
        fd, path = tempfile.mkstemp(suffix=".db")
        os.close(fd)
        os.unlink(path)
        self.db_path = path
        conn = sqlite3.connect(path)
        conn.executescript(SCHEMA)
        self._old_path = dashboard.DB_PATH
        self._old_local = dashboard.db_local
        dashboard.DB_PATH = path
        dashboard.db_local = threading.local()
        self.conn = conn

    def tearDown(self):
        self.conn.close()
        dashboard.DB_PATH = self._old_path
        dashboard.db_local = self._old_local
        os.unlink(self.db_path)

    def seed(self, sid, updated, parent_id=None, directory="/d", archived=None, created_shift_h=1):
        now = int(time.time() * 1000)
        self.conn.execute(
            "INSERT INTO session VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
            (sid, parent_id, "t-" + sid, "agent", None, directory,
             0, 0, 0, 0, 0,
             now - created_shift_h * 3600 * 1000, updated, archived),
        )
        self.conn.commit()

    def flat(self, roots):
        out = {}
        def walk(ns):
            for n in ns:
                out[n["id"]] = n
                walk(n["children"])
        walk(roots)
        return out

    def test_active_marks_sessions_updated_within_15_min(self):
        now = int(time.time() * 1000)
        self.seed("fresh", now - 5 * 60 * 1000)
        self.seed("stale", now - 20 * 60 * 1000)
        nodes = self.flat(dashboard.build_tree())
        self.assertEqual(set(nodes), {"fresh", "stale"})
        self.assertIs(nodes["fresh"]["active"], True)
        self.assertIs(nodes["stale"]["active"], False)

    def test_active_boundary_is_strictly_inside_15_min(self):
        now = int(time.time() * 1000)
        self.seed("edge", now - 15 * 60 * 1000)  # exactly on the window edge: inactive
        self.seed("inside", now - 14 * 60 * 1000)  # just inside the window: active
        nodes = self.flat(dashboard.build_tree())
        self.assertIs(nodes["edge"]["active"], False)
        self.assertIs(nodes["inside"]["active"], True)

    def test_archived_and_dirless_sessions_stay_out_of_the_tree(self):
        now = int(time.time() * 1000)
        self.seed("kept", now)
        self.seed("archived", now, archived=now)
        self.seed("nodir", now, directory="")
        nodes = self.flat(dashboard.build_tree())
        self.assertEqual(set(nodes), {"kept"})

    def msg(self, mid, sid, t, role="assistant", usage=True):
        d = {"role": role}
        if usage:
            d["tokens"] = {"input": 10, "output": 5,
                           "cache": {"read": 0, "write": 0}, "reasoning": 0}
        self.conn.execute(
            "INSERT INTO message VALUES (?,?,?,?,?)",
            (mid, sid, t, t, jdump(d)),
        )
        self.conn.commit()

    def test_beats_list_ai_call_times_inside_window_for_active_sessions(self):
        now = int(time.time() * 1000)
        self.seed("fresh", now - 5 * 60 * 1000)
        self.msg("m1", "fresh", now - 2 * 60 * 1000)
        self.msg("m2", "fresh", now - 10 * 60 * 1000)
        self.msg("m3", "fresh", now - 30 * 60 * 1000)  # outside window
        self.msg("m4", "fresh", now - 60 * 1000, role="user")
        self.msg("m5", "fresh", now - 70 * 1000, usage=False)  # no token usage
        nodes = self.flat(dashboard.build_tree())
        self.assertEqual(nodes["fresh"]["beats"], [now - 10 * 60 * 1000, now - 2 * 60 * 1000])
        self.assertTrue(nodes["fresh"]["active"])

    def test_beats_empty_for_inactive_sessions_even_with_recent_calls(self):
        now = int(time.time() * 1000)
        self.seed("stale", now - 20 * 60 * 1000)
        self.msg("m6", "stale", now - 3 * 60 * 1000)
        nodes = self.flat(dashboard.build_tree())
        self.assertEqual(nodes["stale"]["beats"], [])
        self.assertFalse(nodes["stale"]["active"])


class SessionCallsTest(unittest.TestCase):
    def setUp(self):
        fd, path = tempfile.mkstemp(suffix=".db")
        os.close(fd)
        os.unlink(path)
        self.db_path = path
        conn = sqlite3.connect(path)
        conn.executescript(SCHEMA)
        conn.executemany("INSERT INTO message VALUES (?,?,?,?,?)", [
            user("u1", 1000),
            assistant("a1", 2000),
            assistant("a2", 3000),
            user("u2", 4000),
            assistant("a3", 5000),
            user("u3", 6000),
            assistant("a4", 7000),
            user("u4", 8000),
            assistant("a5", 9000),
        ])
        conn.executemany("INSERT INTO part VALUES (?,?,?,?,?,?)", [
            part("p1", "u1", 1000, {"type": "text", "text": "fix the bug"}),
            part("p2", "u2", 4000, {"type": "text", "synthetic": True,
                                    "text": "Called the Read tool with input: ..."}),
            part("p3", "u2", 4001, {"type": "text",
                                    "text": "<system-reminder>user opened a file</system-reminder>"}),
            part("p4", "u3", 6000, {"type": "text", "text": "thanks"}),
        ])
        conn.commit()
        conn.close()
        self._old_path = dashboard.DB_PATH
        self._old_local = dashboard.db_local
        dashboard.DB_PATH = path
        dashboard.db_local = threading.local()

    def tearDown(self):
        dashboard.DB_PATH = self._old_path
        dashboard.db_local = self._old_local
        os.unlink(self.db_path)

    def test_hu_flags_calls_triggered_by_human_input(self):
        calls = dashboard.session_calls("s1")
        self.assertEqual([c["mid"] for c in calls], ["a1", "a2", "a3", "a4", "a5"])
        self.assertEqual([c["hu"] for c in calls], [True, False, False, True, False])

    def test_calls_keep_token_fields(self):
        calls = dashboard.session_calls("s1")
        self.assertEqual(calls[0]["in"], 10)
        self.assertEqual(calls[0]["out"], 5)
        self.assertEqual(calls[0]["ctx"], 10)
        self.assertTrue(all(isinstance(c["hu"], bool) for c in calls))


class VersionTest(unittest.TestCase):
    def test_version_flag_prints_version_without_touching_db(self):
        buf = io.StringIO()
        old_argv = sys.argv
        old_path = dashboard.DB_PATH
        sys.argv = ["dashboard.py", "--version"]
        dashboard.DB_PATH = "/nonexistent/dashboard-version-test.db"
        try:
            with contextlib.redirect_stdout(buf):
                dashboard.main()
        finally:
            sys.argv = old_argv
            dashboard.DB_PATH = old_path
        self.assertEqual(buf.getvalue(), dashboard.VERSION + "\n")

    def test_load_page_substitutes_version_placeholder(self):
        page = dashboard.load_page()
        self.assertNotIn("{{VERSION}}", page)
        self.assertIn(dashboard.VERSION, page)


class FrozenPageTest(unittest.TestCase):
    def test_load_page_reads_index_from_meipass_when_frozen(self):
        with tempfile.TemporaryDirectory() as d:
            with open(os.path.join(d, "index.html"), "w", encoding="utf-8") as f:
                f.write("frozen page {{VERSION}}")
            saved_frozen = getattr(sys, "frozen", False)
            saved_meipass = getattr(sys, "_MEIPASS", None)
            sys.frozen = True
            sys._MEIPASS = d
            try:
                self.assertEqual(dashboard.load_page(), "frozen page " + dashboard.VERSION)
            finally:
                sys.frozen = saved_frozen
                sys._MEIPASS = saved_meipass


class I18nTest(unittest.TestCase):
    def test_load_i18n_reads_src_i18n_js(self):
        content = dashboard.load_i18n()
        self.assertIn("I18N", content)
        self.assertIn("en", content)
        self.assertIn("ru", content)

    def test_load_i18n_reads_i18n_from_meipass_when_frozen(self):
        with tempfile.TemporaryDirectory() as d:
            with open(os.path.join(d, "i18n.js"), "w", encoding="utf-8") as f:
                f.write("frozen i18n")
            saved_frozen = getattr(sys, "frozen", False)
            saved_meipass = getattr(sys, "_MEIPASS", None)
            sys.frozen = True
            sys._MEIPASS = d
            try:
                self.assertEqual(dashboard.load_i18n(), "frozen i18n")
            finally:
                sys.frozen = saved_frozen
                sys._MEIPASS = saved_meipass


def make_db(path, schema=SCHEMA):
    conn = sqlite3.connect(path)
    conn.executescript(schema)
    conn.commit()
    conn.close()


class ValidateDbTest(unittest.TestCase):
    def test_db_with_session_table_passes(self):
        fd, path = tempfile.mkstemp(suffix=".db")
        os.close(fd)
        os.unlink(path)
        make_db(path)
        try:
            ok, err = dashboard.validate_db(path)
            self.assertTrue(ok)
            self.assertIsNone(err)
        finally:
            os.unlink(path)

    def test_missing_file_fails(self):
        ok, err = dashboard.validate_db(os.path.join(tempfile.gettempdir(), "no-such-opencode.db"))
        self.assertFalse(ok)
        self.assertTrue(err)

    def test_non_sqlite_file_fails(self):
        fd, path = tempfile.mkstemp(suffix=".db")
        os.close(fd)
        with open(path, "wb") as f:
            f.write(b"this is not a sqlite database at all")
        try:
            ok, err = dashboard.validate_db(path)
            self.assertFalse(ok)
            self.assertTrue(err)
        finally:
            os.unlink(path)

    def test_sqlite_without_session_table_fails(self):
        fd, path = tempfile.mkstemp(suffix=".db")
        os.close(fd)
        os.unlink(path)
        conn = sqlite3.connect(path)
        conn.executescript("CREATE TABLE other (x integer);")
        conn.commit()
        conn.close()
        try:
            ok, err = dashboard.validate_db(path)
            self.assertFalse(ok)
            self.assertTrue(err)
        finally:
            os.unlink(path)


class DbCandidatesTest(unittest.TestCase):
    def test_flag_and_env_paths_come_first_in_priority_order(self):
        cands = dashboard.db_candidates(cli_path="/flag/x.db", env_path="/env/x.db")
        self.assertEqual(cands[:2], ["/flag/x.db", "/env/x.db"])

    def test_xdg_default_present_and_env_override_respected(self):
        saved = os.environ.get("XDG_DATA_HOME")
        os.environ["XDG_DATA_HOME"] = "/xdg-home"
        try:
            cands = dashboard.db_candidates(cli_path=None, env_path=None)
            self.assertIn(os.path.join("/xdg-home", "opencode", "opencode.db"), cands)
            self.assertNotIn(os.path.join(os.path.expanduser("~/.local"), "share", "opencode", "opencode.db"), cands)
        finally:
            if saved is None:
                del os.environ["XDG_DATA_HOME"]
            else:
                os.environ["XDG_DATA_HOME"] = saved

    def test_candidates_have_no_duplicates(self):
        cands = dashboard.db_candidates(cli_path="/same.db", env_path="/same.db")
        self.assertEqual(len(cands), len(set(cands)))


class ResolveDbTest(unittest.TestCase):
    def test_first_valid_candidate_is_picked(self):
        fd, path = tempfile.mkstemp(suffix=".db")
        os.close(fd)
        os.unlink(path)
        make_db(path)
        saved = os.environ.get("DB_PATH")
        os.environ["DB_PATH"] = path
        try:
            resolved, found = dashboard.resolve_db(cli_path="/nope/flag.db")
            self.assertTrue(found)
            self.assertEqual(resolved, path)
        finally:
            if saved is None:
                del os.environ["DB_PATH"]
            else:
                os.environ["DB_PATH"] = saved
            os.unlink(path)

    def test_not_found_reports_first_candidate_and_found_false(self):
        # isolate from this machine's real default DB: XDG_DATA_HOME -> empty temp dir,
        # DB_PATH env unset, so the only candidate is the missing --db flag path
        with tempfile.TemporaryDirectory() as td:
            saved_xdg = os.environ.get("XDG_DATA_HOME")
            saved_env = os.environ.get("DB_PATH")
            os.environ["XDG_DATA_HOME"] = td
            os.environ.pop("DB_PATH", None)
            try:
                resolved, found = dashboard.resolve_db(cli_path="/nope/flag.db")
                self.assertFalse(found)
                self.assertEqual(resolved, "/nope/flag.db")
            finally:
                if saved_xdg is None:
                    os.environ.pop("XDG_DATA_HOME", None)
                else:
                    os.environ["XDG_DATA_HOME"] = saved_xdg
                if saved_env is not None:
                    os.environ["DB_PATH"] = saved_env


class SetDbTest(unittest.TestCase):
    def setUp(self):
        self._old_path = dashboard.DB_PATH
        self._old_gen = dashboard.DB_GEN
        self._old_local = dashboard.db_local
        dashboard.db_local = threading.local()
        fd, path = tempfile.mkstemp(suffix=".db")
        os.close(fd)
        os.unlink(path)
        make_db(path)
        self.db_path = path

    def tearDown(self):
        dashboard.DB_PATH = self._old_path
        dashboard.DB_GEN = self._old_gen
        dashboard.db_local = self._old_local
        os.unlink(self.db_path)

    def test_set_db_switches_to_valid_path(self):
        ok, err = dashboard.set_db(self.db_path)
        self.assertTrue(ok)
        self.assertIsNone(err)
        self.assertEqual(dashboard.DB_PATH, self.db_path)
        self.assertEqual(dashboard.DB_GEN, self._old_gen + 1)
        self.assertIsNotNone(dashboard.db().execute("SELECT count(*) FROM session").fetchone())

    def test_set_db_rejects_invalid_path_and_keeps_state(self):
        ok, err = dashboard.set_db("/nope/does-not-exist.db")
        self.assertFalse(ok)
        self.assertTrue(err)
        self.assertEqual(dashboard.DB_PATH, self._old_path)
        self.assertEqual(dashboard.DB_GEN, self._old_gen)

    def test_db_reconnects_after_generation_bump(self):
        fd, path2 = tempfile.mkstemp(suffix=".db")
        os.close(fd)
        os.unlink(path2)
        make_db(path2)
        try:
            conn1 = dashboard.db()  # thread-local conn bound to old path
            dashboard.set_db(path2)
            conn2 = dashboard.db()
            self.assertIsNot(conn1, conn2)
            row = conn2.execute("SELECT count(*) AS n FROM sqlite_master").fetchone()
            self.assertIsNotNone(row)
        finally:
            os.unlink(path2)


class ApiDbHttpTest(unittest.TestCase):
    """HTTP-level tests against a real server on an ephemeral port."""

    @staticmethod
    def http_get(url):
        with urllib.request.urlopen(url) as r:
            return r.status, json.loads(r.read())

    @staticmethod
    def http_post(url, body):
        req = urllib.request.Request(url, data=json.dumps(body).encode(),
                                    headers={"Content-Type": "application/json"})
        try:
            with urllib.request.urlopen(req) as r:
                return r.status, json.loads(r.read())
        except urllib.error.HTTPError as e:
            return e.code, json.loads(e.read())

    def setUp(self):
        self._old_path = dashboard.DB_PATH
        self._old_gen = dashboard.DB_GEN
        self._old_local = dashboard.db_local
        dashboard.db_local = threading.local()
        dashboard.DB_PATH = "/no-such-db/api-test.db"
        self.srv = ThreadingHTTPServer(("127.0.0.1", 0), dashboard.Handler)
        self.url = f"http://127.0.0.1:{self.srv.server_address[1]}"
        threading.Thread(target=self.srv.serve_forever, daemon=True).start()

    def tearDown(self):
        self.srv.shutdown()
        self.srv.server_close()
        dashboard.DB_PATH = self._old_path
        dashboard.DB_GEN = self._old_gen
        dashboard.db_local = self._old_local

    def test_api_db_reports_not_found_state(self):
        status, body = self.http_get(self.url + "/api/db")
        self.assertEqual(status, 200)
        self.assertFalse(body["found"])
        self.assertEqual(body["path"], "/no-such-db/api-test.db")
        self.assertTrue(body["candidates"])

    def test_tree_returns_db_not_found_error_when_no_db(self):
        try:
            with urllib.request.urlopen(self.url + "/api/tree") as r:
                status, body = r.status, json.loads(r.read())
        except urllib.error.HTTPError as e:
            status, body = e.code, json.loads(e.read())
        self.assertEqual(status, 503)
        self.assertEqual(body["error"], "db_not_found")

    def test_post_db_switches_to_valid_path(self):
        fd, path = tempfile.mkstemp(suffix=".db")
        os.close(fd)
        os.unlink(path)
        make_db(path)
        try:
            status, body = self.http_post(self.url + "/api/db", {"path": path})
            self.assertEqual(status, 200)
            self.assertTrue(body["ok"])
            self.assertEqual(body["path"], path)
            status, body = self.http_get(self.url + "/api/db")
            self.assertTrue(body["found"])
            self.assertEqual(body["path"], path)
            status, body = self.http_get(self.url + "/api/tree")
            self.assertEqual(status, 200)
            self.assertEqual(body, [])
        finally:
            os.unlink(path)

    def test_post_db_rejects_bad_path_with_400(self):
        status, body = self.http_post(self.url + "/api/db", {"path": "/nope/bad.db"})
        self.assertEqual(status, 400)
        self.assertFalse(body["ok"])
        self.assertTrue(body["error"])


class MainBindTest(unittest.TestCase):
    def test_main_reports_port_in_use_without_traceback(self):
        # occupy the port and leave no fallback ports (PORT_TRIES=1):
        # main() must give a clear message, not a traceback
        with socketserver.TCPServer(("127.0.0.1", 0), socketserver.BaseRequestHandler) as blocker:
            port = blocker.server_address[1]
            saved_port, saved_tries = dashboard.PORT, dashboard.PORT_TRIES
            dashboard.PORT, dashboard.PORT_TRIES = port, 1
            buf = io.StringIO()
            old_argv = sys.argv
            sys.argv = ["dashboard.py"]
            try:
                with contextlib.redirect_stdout(buf):
                    dashboard.main()
            finally:
                sys.argv = old_argv
                dashboard.PORT, dashboard.PORT_TRIES = saved_port, saved_tries
            self.assertNotIn("Traceback", buf.getvalue())
            self.assertIn("Address already in use", buf.getvalue())
            self.assertIn(str(port), buf.getvalue())

    def test_main_falls_back_to_next_free_port(self):
        # default port busy: main() must bind the next free port and say so
        with socketserver.TCPServer(("127.0.0.1", 0), socketserver.BaseRequestHandler) as blocker:
            port = blocker.server_address[1]
            saved_port = dashboard.PORT
            dashboard.PORT = port
            buf = io.StringIO()
            old_argv = sys.argv
            old_nobrowser = os.environ.get("NO_BROWSER")
            os.environ["NO_BROWSER"] = "1"
            sys.argv = ["dashboard.py"]
            def run():
                with contextlib.redirect_stdout(buf):
                    dashboard.main()

            t = threading.Thread(target=run, daemon=True)
            try:
                t.start()
                deadline = time.time() + 5
                while time.time() < deadline and dashboard._main_srv is None:
                    time.sleep(0.02)
                srv = dashboard._main_srv
                self.assertIsNotNone(srv, "main() must start a server even when the port is busy")
                chosen = srv.server_address[1]
                self.assertGreater(chosen, port, "must pick a port after the busy one")
                self.assertIn(str(chosen), buf.getvalue())
                self.assertIn("already in use", buf.getvalue())
                with urllib.request.urlopen(f"http://127.0.0.1:{chosen}/api/db") as r:
                    self.assertEqual(r.status, 200)
            finally:
                sys.argv = old_argv
                os.environ.pop("NO_BROWSER", None)
                if old_nobrowser is not None:
                    os.environ["NO_BROWSER"] = old_nobrowser
                if dashboard._main_srv is not None:
                    dashboard._main_srv.shutdown()
                    dashboard._main_srv.server_close()
                    dashboard._main_srv = None
                dashboard.PORT = saved_port


if __name__ == "__main__":
    unittest.main()
