"""Tests for dashboard.py — run: python3 test/test_dashboard.py"""

import contextlib
import io
import json
import os
import sqlite3
import sys
import tempfile
import threading
import unittest

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


if __name__ == "__main__":
    unittest.main()
