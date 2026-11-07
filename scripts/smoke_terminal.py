#!/usr/bin/env python3
"""Exercise the real binary against a pseudo-terminal (no Rex required)."""
import base64
import copy
import fcntl
import os
from pathlib import Path
import pty
import select
import signal
import struct
import subprocess
import sys
import tempfile
import termios
import time

BINARY = str(Path(sys.argv[1] if len(sys.argv) > 1 else "bin/rex-md-viewer").resolve())
QUERY = b"i=2147483000"
SOURCE = "# 日本語\n\n" + "日本語の本文と English **bold**.\n\n" * 2000


def exercise(ending, supported=True):
    with tempfile.TemporaryDirectory(prefix="rex-md-pty-") as temp:
        path = Path(temp) / "日本語 ' $.md"
        path.write_text(SOURCE)
        master, slave = pty.openpty()
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 24, 80, 640, 432))
        before = copy.deepcopy(termios.tcgetattr(slave))
        env = dict(os.environ)
        env.pop("REX_MD_VIEWER_TOKEN", None)
        process = subprocess.Popen(
            [BINARY, "view", "--in-place", "--", str(path)],
            stdin=slave, stdout=slave, stderr=slave, env=env,
        )
        data = bytearray()
        query_answered = False

        def pump():
            nonlocal query_answered
            if select.select([master], [], [], 0.03)[0]:
                try:
                    block = os.read(master, 65536)
                except OSError:
                    block = b""
                data.extend(block)
            if not query_answered and QUERY in data:
                query_answered = True
                if supported:
                    os.write(master, b"\x1b_Gi=2147483000;OK\x1b\\")
                    os.write(master, b"\x1b]10;rgb:eeee/eeee/eeee\x1b\\\x1b]11;rgb:1111/1111/1111\x1b\\")

        def wait_for(predicate, timeout=6):
            deadline = time.monotonic() + timeout
            while time.monotonic() < deadline:
                pump()
                if predicate(bytes(data)):
                    return
                if process.poll() is not None:
                    break
            raise AssertionError(f"{ending}: condition failed; exit={process.poll()}, tail={bytes(data[-300:])!r}")

        try:
            if not supported:
                wait_for(lambda _: process.poll() is not None)
                assert process.returncode == 1
                assert b"Kitty Graphics Protocol" in data
            else:
                wait_for(lambda d: "表示中".encode() in d)
                assert b"a=p," in data
                # Every upload is a PNG of the viewport, never the tall document.
                start = data.index(b"a=t,")
                payload = bytearray()
                cursor = data.rfind(b"\x1b_G", 0, start + 1)
                while True:
                    end = data.index(b"\x1b\\", cursor)
                    header, chunk = bytes(data[cursor + 3:end]).split(b";", 1)
                    payload.extend(chunk)
                    if b"m=0" in header:
                        break
                    cursor = data.index(b"\x1b_G", end + 2)
                png = base64.b64decode(payload)
                assert png[:8] == b"\x89PNG\r\n\x1a\n"
                width, height = struct.unpack(">II", png[16:24])
                assert (width, height) == (640, 396), (width, height)

                data.clear()
                os.write(master, b"G")
                wait_for(lambda d: b"a=p," in d)
                # Resize and confirm a fresh viewport has been transmitted.
                data.clear()
                fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 20, 60, 480, 360))
                wait_for(lambda d: b"c=60,r=18," in d)

                # The same file size and mtime must not suppress content changes.
                stamp = path.stat()
                data.clear()
                started = time.monotonic()
                path.write_text(SOURCE.replace("English", "Changed"))
                os.utime(path, ns=(stamp.st_atime_ns, stamp.st_mtime_ns))
                wait_for(lambda d: "更新 ".encode() in d)
                elapsed = time.monotonic() - started
                print(f"PTY update ({len(SOURCE.encode())} bytes): {elapsed:.3f}s")
                assert elapsed < 1.0, elapsed

                data.clear()
                path.unlink()
                wait_for(lambda d: "読み込みエラー".encode() in d)
                data.clear()
                replacement = Path(temp) / "save.tmp"
                replacement.write_text(SOURCE)
                replacement.replace(path)
                wait_for(lambda d: "更新 ".encode() in d)

                # A theme reply triggers a redraw without changing the file.
                data.clear()
                os.write(master, b"\x1b]10;rgb:1111/1111/1111\x1b\\\x1b]11;rgb:ffff/ffff/ffff\x1b\\")
                wait_for(lambda d: b"a=p," in d)
                data.clear()
                os.write(master, b"r")
                wait_for(lambda d: "再読み込み".encode() in d)
                if ending == "q":
                    os.write(master, b"q")
                elif ending == "ctrl-c":
                    os.write(master, b"\x03")
                else:
                    process.send_signal(signal.SIGTERM)
                wait_for(lambda _: process.poll() is not None)
                assert process.returncode == 0
            # The binary must undo raw mode and terminal display modes.
            after = termios.tcgetattr(slave)
            # macOS sets PENDIN when canonical input is restored. It is a
            # transient kernel flag, not an application terminal preference.
            pending = getattr(termios, "PENDIN", 0)
            before[3] &= ~pending
            after[3] &= ~pending
            assert after == before, f"termios not restored: before={before!r}, after={after!r}"
            assert b"\x1b[?1049l" in data and b"\x1b[?25h" in data
            if supported:
                assert b"a=d,d=I," in data
            print(f"PASS terminal cleanup: {ending}, supported={supported}")
        finally:
            if process.poll() is None:
                process.kill()
            process.wait()
            os.close(master)
            os.close(slave)


if __name__ == "__main__":
    for ending in ("q", "ctrl-c", "sigterm"):
        exercise(ending)
    exercise("unsupported", supported=False)
