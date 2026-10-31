#!/usr/bin/env python3
# SPDX-License-Identifier: Apache-2.0
"""Exercise a real terminal confirmation without emitting terminal contents."""
import os
import select
import subprocess
import sys
import time


def confirm(argv, answer):
    import pty
    master, slave = pty.openpty()
    child = None
    prompted = False
    try:
        child = subprocess.Popen(argv, stdin=slave, stdout=slave, stderr=slave,
                                 start_new_session=True, close_fds=True)
        os.close(slave)
        slave = None
        output = b""
        deadline = time.monotonic() + 20
        while child.poll() is None:
            if time.monotonic() > deadline:
                raise RuntimeError("terminal deadline")
            if select.select([master], [], [], 0.05)[0]:
                try:
                    output = (output + os.read(master, 65536))[-65536:]
                except OSError:
                    break
                if not prompted and b"Apply this shell configuration change?" in output:
                    # The proposal must precede the prompt, not merely exist in
                    # a log emitted after authorization.
                    if len(output.split(b"Apply this shell configuration change?")[0]) < 40:
                        raise RuntimeError("missing proposal")
                    os.write(master, answer.encode("ascii") + b"\n")
                    prompted = True
        code = child.wait(timeout=2)
        if not prompted or (answer == "yes" and code != 0) or (answer == "no" and code == 0):
            raise RuntimeError("confirmation contract failed")
    finally:
        if child is not None and child.poll() is None:
            child.terminate()
            try:
                child.wait(timeout=6)
            except subprocess.TimeoutExpired:
                child.kill()
                child.wait(timeout=2)
        if slave is not None:
            os.close(slave)
        os.close(master)


if __name__ == "__main__":
    try:
        if sys.argv[1] not in ("yes", "no"):
            raise ValueError("unsupported response")
        confirm(sys.argv[2:], sys.argv[1])
        print("native-terminal-confirmation-passed")
    except Exception:
        print("native-terminal-confirmation-unavailable", file=sys.stderr)
        sys.exit(1)
