#!/usr/bin/env python3
"""Drive the helper without a compositor, session bus, devices or real home."""

import json
import os
import pathlib
import subprocess
import sys
import tempfile
import time


def run(binary, frames):
    with tempfile.TemporaryDirectory() as temp:
        runtime = pathlib.Path(temp) / "run"
        runtime.mkdir(mode=0o700)
        env = {
            "PATH": os.environ["PATH"],
            "HOME": temp,
            "GOCACHE": str(pathlib.Path(temp) / "cache"),
            "XDG_RUNTIME_DIR": str(runtime),
            "DBUS_SESSION_BUS_ADDRESS": f"unix:path={runtime}/no-bus",
            "QT_QPA_PLATFORM": "offscreen",
        }
        if "QT_PLUGIN_PATH" in os.environ:
            env["QT_PLUGIN_PATH"] = os.environ["QT_PLUGIN_PATH"]
        return subprocess.run([binary], input=frames, capture_output=True, env=env, timeout=5)


def line(message):
    return (json.dumps(message) + "\n").encode()


def main(binary):
    text = "fixture secret\nwith <markup> and £"
    preview = {"session": "fixture", "stable": text}
    status = {"session": "fixture", "capture": "dictating"}
    result = run(binary, line({"type": "view", "status": status, "preview": preview}) + line({
        "type": "copy", "id": 12, "text": text, "deadline_ms": int(time.time() * 1000) + 4000,
    }))
    assert result.returncode == 0, result.stderr
    frames = [json.loads(response) for response in result.stdout.splitlines()]
    assert frames[0] == {"type": "ready"}, frames
    assert {"type": "copied", "id": 12, "code": "unavailable"} in frames, frames
    assert b"fixture secret" not in result.stdout + result.stderr

    result = run(binary, b"x" * (1 << 20) + b"\n")
    assert result.returncode == 1, "oversize frame did not close the helper"


if __name__ == "__main__":
    main(sys.argv[1])
