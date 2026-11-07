#!/usr/bin/env python3
"""Opt-in integration smoke test. Creates/closes only this test's Rex viewer."""
from concurrent.futures import ThreadPoolExecutor
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import time

BINARY = str(Path(sys.argv[1] if len(sys.argv) > 1 else "bin/rex-md-viewer").resolve())
SESSION = os.environ["REX_SESSION"]


def rex(*args):
    return subprocess.check_output(
        ["rex", "-S", os.environ["REX_SERVER"], "--autostart=false", *args], text=True
    )


def focused():
    inspected = json.loads(rex("session", "inspect", SESSION, "--json"))
    return {w["window_id"]: w.get("focused_block_id") for w in inspected["windows"]}


with tempfile.TemporaryDirectory(prefix="rex-md-smoke-") as temp:
    file = Path(temp) / "日本語 ' ${x}; $(not-a-command).md"
    source = "# 日本語\n\n- [ ] 未完了\n- [~] 作業中\n- [x] 完了\n\n" + "日本語の本文と English **bold**.\n\n" * 1500
    file.write_text(source)
    env = dict(os.environ, XDG_STATE_HOME=str(Path(temp) / "state home"))

    def viewer(*args):
        return subprocess.check_output([BINARY, *args], env=env, text=True).strip()

    before = focused()
    block = None
    try:
        with ThreadPoolExecutor(max_workers=3) as pool:
            ids = list(pool.map(lambda _: viewer("view", "--below", "--ratio", "30", "--", str(file)), range(3)))
        assert len(set(ids)) == 1, ids
        block = ids[0]
        assert focused() == before, "focus changed"
        print(f"PASS concurrent split, reuse, special-character path, focus: {block}")

        def capture():
            return rex("capture", "-s", SESSION, "-b", block, "--trim")

        def wait_for(predicate):
            deadline = time.monotonic() + 4
            while time.monotonic() < deadline:
                content = capture()
                if predicate(content):
                    return content
                time.sleep(0.04)
            raise AssertionError(content)

        content = wait_for(lambda c: "表示中" in c)
        stamp = file.stat()
        start = time.monotonic()
        file.write_text(source.replace("English", "Changed"))
        os.utime(file, ns=(stamp.st_atime_ns, stamp.st_mtime_ns))
        content = wait_for(lambda c: "更新 " in c)
        elapsed = time.monotonic() - start
        print(f"PASS real Rex update ({len(source.encode())} bytes): {elapsed:.3f}s")
        assert elapsed < 1.0, elapsed
        rex("send", "-s", SESSION, "-b", block, "G")
        content = wait_for(lambda c: bool(re.search(r"(\d+)/\1", c)))
        file.unlink()
        wait_for(lambda c: "読み込みエラー" in c)
        replacement = Path(temp) / "save.tmp"
        replacement.write_text(source)
        replacement.replace(file)
        wait_for(lambda c: "更新 " in c)
        print("PASS scroll, deletion, atomic-save recovery")
    finally:
        if block:
            viewer("hide", "--", str(file))
            viewer("hide", "--", str(file))
            blocks = json.loads(rex("block", "ls", "-s", SESSION, "--json"))["blocks"]
            assert block not in [b["block_id"] for b in blocks]
            print("PASS hide and repeated hide")
