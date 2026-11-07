#!/usr/bin/env python3
"""Test Rex's real input encoder using the Go test helper's recording opener.

This does not exercise macOS's physical mouse/key handling or launch a browser.
"""
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time

BINARY = str(Path(sys.argv[1]).resolve())
SESSION = os.environ["REX_SESSION"]
ANCHOR = os.environ["REX_BLOCK"]


def rex(*args):
    return subprocess.check_output(["rex", "--autostart=false", *args], text=True)


with tempfile.TemporaryDirectory(prefix="rex-md-link-rex-") as temp:
    path = Path(temp) / "links.md"
    path.write_text("[click here](https://example.com/first) [next link](https://example.com/second)\n")
    block = None
    try:
        env = dict(os.environ, REX_MD_LINK_TEST_FILE=str(path))
        env.pop("REX_MD_VIEWER_TOKEN", None)
        created = json.loads(subprocess.check_output([
            "rex", "--autostart=false", "split", "-s", SESSION, "-b", ANCHOR,
            "--split=below", "--ratio", "25", "--focus=false", "--shell=none", "--json",
            "--", "env", "REX_MD_LINK_TEST_FILE=" + str(path), BINARY, "-test.run=^TestLinkViewerHelper$",
        ], env=env, text=True))
        block = created["block_ids"][0]

        def capture():
            return rex("capture", "-s", SESSION, "-b", block, "--trim")

        def wait_for(predicate):
            deadline = time.monotonic() + 6
            while time.monotonic() < deadline:
                if predicate():
                    return
                time.sleep(.05)
            raise AssertionError(capture())

        wait_for(lambda: "表示中" in capture())
        size = json.loads(rex("block", "call", "-s", SESSION, "-b", block, "size"))
        cell_w, cell_h = size["cell_width_px"], size["cell_height_px"]
        points = json.loads(Path(str(path) + ".points").read_text())

        def event(kind, payload):
            args = {"session_id": SESSION, "block_id": block, "event": {"type": kind, "payload": payload}}
            rex("api", "call", "block.event", json.dumps(args))

        def cmd(action):
            event("key", {"key": "meta_left", "mods": "super" if action == "press" else "",
                          "consumed_mods": "", "text": "", "unshifted_codepoint": 0,
                          "composing": False, "action": action})

        def click(point):
            x, y = point
            for action in ("press", "release"):
                if action == "release":
                    event("focus", {"action": "in"})
                event("mouse", {"button": "left", "action": action, "mods": "super",
                                "x": x // cell_w, "y": (y + cell_h) // cell_h})

        def opened():
            log = Path(str(path) + ".opened")
            return [json.loads(line) for line in log.read_text().splitlines()] if log.exists() else []

        # Command is absent from the mouse encoder. It must arrive through the
        # standalone keyboard modifier events, not an invented SGR modifier bit.
        click(points[0])
        time.sleep(.15)
        assert opened() == [], opened()
        for point, target in zip(points[:2], ("first", "second")):
            cmd("press")
            click(point)
            wait_for(lambda: opened() and opened()[-1] == "https://example.com/" + target)
            wait_for(lambda: "リンクをブラウザへ渡しました" in capture())
            cmd("release")
        assert opened() == ["https://example.com/first", "https://example.com/second"], opened()
        print("PASS Rex pixel mouse + standalone Command encoder, distinct targets, plain-click rejection")
        rex("send", "-s", SESSION, "-b", block, "q")
        wait_for(lambda: block not in [b["block_id"] for b in json.loads(rex("block", "ls", "-s", SESSION, "--json"))["blocks"]])
        print("PASS Rex link viewer cleanup (recording opener; no browser launched)")
        block = None
    finally:
        if block:
            rex("block", "close", "-s", SESSION, block)
