#!/usr/bin/env python3
"""Compare real PTY uploads to reference PNGs from the Go test helper."""
import base64
import fcntl
import os
from pathlib import Path
import pty
import re
import select
import struct
import subprocess
import sys
import tempfile
import termios
import time

BINARY = sys.argv[1]
SOURCE = "# Scale 日本語\n\n- [ ] Pending\n- [x] Done\n\n" + "日本語 English text with **bold** and `code`.\n\n" * 300


def exercise(scale):
    with tempfile.TemporaryDirectory(prefix="rex-md-scale-pty-") as temp:
        path = Path(temp) / "scale.md"
        path.write_text(SOURCE)
        updated = SOURCE.replace("English", "Changed")
        Path(str(path) + ".updated").write_text(updated)
        master, slave = pty.openpty()

        def resize(rows, cols, width, height):
            fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", rows, cols, width, height))

        resize(24, 80, 640, 432)
        env = dict(os.environ, REX_MD_SCALE_TEST_FILE=str(path), REX_MD_SCALE_TEST_VALUE=scale,
                   REX_MD_VIEWER_SCALE="invalid server preference")
        env.pop("REX_MD_VIEWER_TOKEN", None)
        env["GORACE"] = env.get("GORACE", "") + " atexit_sleep_ms=0"
        proc = subprocess.Popen([BINARY, "-test.run=^TestScaleViewerHelper$"],
                                stdin=slave, stdout=slave, stderr=slave, env=env)
        data = bytearray()
        answered = False

        def pump():
            nonlocal answered
            if select.select([master], [], [], .02)[0]:
                data.extend(os.read(master, 65536))
            if not answered and b"i=2147483000" in data:
                answered = True
                os.write(master, b"\x1b_Gi=2147483000;OK\x1b\\")

        def uploads():
            payload = bytearray()
            for header, chunk in re.findall(rb"\x1b_G([^;]*);([^\x1b]*)\x1b\\", data):
                if b"a=t," in header:
                    payload.clear()
                if b"m=" in header:
                    payload.extend(chunk)
                    if b"m=0" in header:
                        yield base64.b64decode(payload)
                        payload.clear()

        def wait_for(predicate):
            deadline = time.monotonic() + 6
            while time.monotonic() < deadline:
                pump()
                if predicate():
                    return
                if proc.poll() is not None:
                    break
            raise AssertionError(f"scale={scale}, exit={proc.poll()}, tail={bytes(data[-300:])!r}")

        def frame(name, status=None):
            expected = Path(str(path) + "." + name + ".png").read_bytes()
            wait_for(lambda: b"q:quit" in data and expected in uploads()
                     and (status is None or status in data))

        def send(key):
            data.clear()
            os.write(master, key)

        try:
            wait_for(lambda: b"Viewing" in data)
            frame("initial")
            send(b"j")
            frame("scroll")
            send(b" ")
            frame("page")
            send(b"b")
            frame("scroll")
            send(b"k")
            frame("initial")
            send(b"G")
            wait_for(lambda: bool(re.search(rb"\b(\d+)/\1\b", data)))
            send(b"g")
            frame("initial")
            data.clear()
            resize(24, 80, 640, 192)
            frame("small-cells")
            data.clear()
            resize(20, 60, 480, 360)
            frame("resize")
            data.clear()
            path.write_text(updated)
            frame("updated", b"Updated")
            send(b"r")
            frame("updated", b"Reloaded")
            send(b"q")
            wait_for(lambda: proc.poll() is not None)
            assert proc.returncode == 0
            print(f"PASS scale={scale}: exact viewport PNG, scroll, page, cell size, resize, save, reload")
        finally:
            if proc.poll() is None:
                proc.kill()
            proc.wait()
            os.close(master)
            os.close(slave)


for scale in ("0.5", "1", "1.2", "4"):
    exercise(scale)
