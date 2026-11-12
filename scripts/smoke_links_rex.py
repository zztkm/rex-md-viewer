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
        env = dict(os.environ, REX_MD_LINK_TEST_FILE=str(path), REX_MD_VIEWER_SCALE="1.2")
        env.pop("REX_MD_VIEWER_TOKEN", None)
        created = json.loads(subprocess.check_output([
            "rex", "--autostart=false", "split", "-s", SESSION, "-b", ANCHOR,
            "--split=below", "--ratio", "25", "--focus=false", "--shell=none", "--json",
            "--", "env", "REX_MD_LINK_TEST_FILE=" + str(path), "REX_MD_VIEWER_SCALE=1.2", BINARY, "-test.run=^TestLinkViewerHelper$",
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
            diagnostic = {suffix: Path(str(path) + suffix).read_text()
                          for suffix in (".geometry", ".points", ".opened") if Path(str(path) + suffix).exists()}
            raise AssertionError(f"{capture()}\ngeometry and links={diagnostic}\n"
                                 f"current size={rex('block', 'call', '-s', SESSION, '-b', block, 'size')}")

        def opened():
            log = Path(str(path) + ".opened")
            return [json.loads(line) for line in log.read_text().splitlines()] if log.exists() else []

        def terminal_size():
            return json.loads(rex("block", "call", "-s", SESSION, "-b", block, "size"))

        def dimensions(size):
            return size["columns"], size["rows"], size["cell_width_px"], size["cell_height_px"]

        wait_for(lambda: "Viewing" in capture())
        previous_size, stable_since = None, time.monotonic()
        deadline = time.monotonic() + 6
        while time.monotonic() < deadline:
            size = terminal_size()
            if dimensions(size) != previous_size:
                previous_size, stable_since = dimensions(size), time.monotonic()
            if size["owner"] is not None and time.monotonic() - stable_since >= .25:
                break
            time.sleep(.04)
        else:
            raise AssertionError(f"Rex terminal size did not settle: {size}")
        rex("send", "-s", SESSION, "-b", block, "r")
        wait_for(lambda: "Reloaded" in capture())
        cell_w, cell_h = size["cell_width_px"], size["cell_height_px"]
        geometry = [size["columns"], size["rows"], size["columns"] * cell_w, size["rows"] * cell_h]
        subprocess.check_output([BINARY, "-test.run=^TestLinkViewerHelper$"],
                                env=dict(env, REX_MD_LINK_TEST_GEOMETRY=json.dumps(geometry)))
        current = terminal_size()
        assert dimensions(current) == dimensions(size), f"terminal resized while calculating click points: {dimensions(size)} -> {dimensions(current)}"
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

        # Command is absent from the mouse encoder. It must arrive through the
        # standalone keyboard modifier events, not an invented SGR modifier bit.
        click(points[0])
        time.sleep(.15)
        assert opened() == [], opened()
        for point, target in zip(points[:2], ("first", "second")):
            cmd("press")
            click(point)
            wait_for(lambda: opened() and opened()[-1] == "https://example.com/" + target)
            wait_for(lambda: "Link sent to browser" in capture())
            cmd("release")
        assert opened() == ["https://example.com/first", "https://example.com/second"], opened()
        print("PASS Rex scale 1.2 pixel mouse + standalone Command encoder, distinct targets, plain-click rejection")
        rex("send", "-s", SESSION, "-b", block, "q")
        wait_for(lambda: block not in [b["block_id"] for b in json.loads(rex("block", "ls", "-s", SESSION, "--json"))["blocks"]])
        print("PASS Rex link viewer cleanup (recording opener; no browser launched)")
        block = None
    finally:
        if block:
            rex("block", "close", "-s", SESSION, block)
