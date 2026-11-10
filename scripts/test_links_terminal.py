#!/usr/bin/env python3
"""PTY integration against the Go test helper, with a recording browser stub."""
import copy
import fcntl
import json
import os
from pathlib import Path
import pty
import select
import struct
import subprocess
import sys
import tempfile
import termios
import time

BINARY = sys.argv[1]
SOURCE = "[click](https://example.com/first)[next](https://example.com/second)\n\n" + "body text\n\n" * 100


def exercise(capable):
    with tempfile.TemporaryDirectory(prefix="rex-md-link-pty-") as temp:
        path = Path(temp) / "links.md"
        path.write_text(SOURCE)
        master, slave = pty.openpty()
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 24, 80, 640, 432))
        before = copy.deepcopy(termios.tcgetattr(slave))
        env = dict(os.environ, REX_MD_LINK_TEST_FILE=str(path))
        # The race runtime normally waits one second at process exit. Disable
        # only that delay so the responsiveness assertion measures the viewer.
        env["GORACE"] = env.get("GORACE", "") + " atexit_sleep_ms=0"
        env.pop("REX_MD_VIEWER_TOKEN", None)
        proc = subprocess.Popen([BINARY, "-test.run=^TestLinkViewerHelper$"],
                                stdin=slave, stdout=slave, stderr=slave, env=env)
        data = bytearray()
        history = bytearray()
        answered = False

        def pump():
            nonlocal answered
            if select.select([master], [], [], .02)[0]:
                try:
                    block = os.read(master, 65536)
                except OSError:
                    block = b""
                data.extend(block)
                history.extend(block)
            if not answered and b"i=2147483000" in data:
                answered = True
                os.write(master, b"\x1b_Gi=2147483000;OK\x1b\\")
                os.write(master, b"\x1b[?1016;1$y\x1b[?11u" if capable else b"\x1b[?1016;0$y\x1b[?3u")

        def wait_for(predicate, timeout=5):
            deadline = time.monotonic() + timeout
            while time.monotonic() < deadline:
                pump()
                if predicate():
                    return
                if proc.poll() is not None:
                    break
            raise AssertionError(f"capable={capable}, exit={proc.poll()}, tail={bytes(data[-400:])!r}")

        def opened():
            log = Path(str(path) + ".opened")
            return [json.loads(line) for line in log.read_text().splitlines()] if log.exists() else []

        def send(b):
            os.write(master, b)

        def idle():
            until = time.monotonic() + .15
            while time.monotonic() < until:
                pump()

        def pointer(point, release=False, code=0):
            x, y = point
            send(f"\x1b[<{code};{x+1};{y+19}{'m' if release else 'M'}".encode())

        def click(point, cmd=True):
            send(b"\x1b[57444;9u" if cmd else b"\x1b[57444;1:3u")
            pointer(point)
            # Physical clicks in Rex can repeat focus-in before release.
            send(b"\x1b[I")
            pointer(point, release=True)

        def change(destination):
            data.clear()
            path.write_text(SOURCE.replace("/first", "/" + destination))
            wait_for(lambda: "Updated ".encode() in data)
            idle()

        try:
            wait_for(lambda: "Viewing".encode() in data)
            points = json.loads(Path(str(path) + ".points").read_text())
            first, second = points[:2]
            assert b"\x1b[>11u" in history and b"\x1b[?1016h" in history
            click(first, cmd=False)
            pointer(first, release=True)
            idle()
            assert opened() == [], opened()
            if not capable:
                click(first)
                idle()
                assert opened() == [], "unsupported pointer mode opened a link"
            else:
                # Correct targets, once per press/release, including adjacent links.
                for point, target in [(first, "first"), (second, "second")]:
                    data.clear()
                    count = len(opened())
                    click(point)
                    wait_for(lambda: len(opened()) == count + 1)
                    wait_for(lambda: "Link sent to browser".encode() in data)
                    assert opened()[-1] == "https://example.com/" + target, opened()
                    pointer(point, release=True)
                    idle()
                    assert len(opened()) == count + 1

                count = len(opened())
                # Drag, wrong button, header, footer, whitespace and focus loss.
                send(b"\x1b[57444;9u")
                pointer(first)
                pointer((first[0] + 1, first[1]), code=32)
                pointer(first, release=True)
                pointer(first, code=2)
                pointer(first, release=True, code=2)
                for point in [(0, 0), (639, 200), (first[0], -18), (first[0], 400)]:
                    click(point)
                send(b"\x1b[57444;9u\x1b[O\x1b[I")
                pointer(first)
                pointer(first, release=True)
                idle()
                assert len(opened()) == count, opened()

                # Updating a destination cancels the old gesture atomically.
                send(b"\x1b[57444;9u")
                pointer(first)
                change("other")
                pointer(first, release=True)
                idle()
                assert len(opened()) == count
                data.clear()
                click(first)
                wait_for(lambda: len(opened()) == count + 1)
                wait_for(lambda: "Link sent to browser".encode() in data)
                assert opened()[-1] == "https://example.com/other"
                count += 1

                # A browser error leaves the viewer usable.
                change("fail")
                data.clear()
                click(first)
                wait_for(lambda: "Cannot open link: test browser failure".encode() in data)
                assert len(opened()) == count + 1
                count += 1

                # Removal and scrolling must not leave old clickable positions.
                data.clear()
                path.write_text("no links\n\n" + "body text\n\n" * 100)
                wait_for(lambda: "Updated ".encode() in data)
                click(first)
                idle()
                assert len(opened()) == count
                change("other")
                send(b"\x1b[57444;9u")
                pointer(first)
                data.clear()
                send(b"\x1b[106u")
                pointer(first, release=True)
                wait_for(lambda: b"a=p," in data)
                idle()
                assert len(opened()) == count
                data.clear()
                send(b"\x1b[103u")
                wait_for(lambda: b"a=p," in data)
                idle()

                # Resize mid-gesture cancels it, rather than hit-testing old geometry.
                send(b"\x1b[57444;9u")
                pointer(first)
                data.clear()
                fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 24, 100, 800, 432))
                wait_for(lambda: b"c=100,r=22," in data)
                pointer(first, release=True)
                idle()
                assert len(opened()) == count
                fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack("HHHH", 24, 80, 640, 432))
                data.clear()
                wait_for(lambda: b"c=80,r=22," in data)
                idle()

                # The opener waits three seconds. Quit must not wait for it.
                change("slow")
                click(first)
                wait_for(lambda: len(opened()) == count + 1)

            start = time.monotonic()
            send(b"\x1b[113u")
            wait_for(lambda: proc.poll() is not None)
            assert proc.returncode == 0, bytes(data[-400:])
            if capable:
                assert time.monotonic() - start < 1, "browser launch blocked quit"
            after = termios.tcgetattr(slave)
            pending = getattr(termios, "PENDIN", 0)
            before[3] &= ~pending
            after[3] &= ~pending
            assert before == after, "raw mode not restored"
            for mode in (1002, 1004, 1006, 1016):
                assert f"\x1b[?{mode}l".encode() in history
            assert b"\x1b[<u" in history and b"\x1b[?1049l" in history
            print(f"PASS link PTY integration, capable={capable}")
        finally:
            if proc.poll() is None:
                proc.kill()
            proc.wait()
            os.close(master)
            os.close(slave)


exercise(True)
exercise(False)
