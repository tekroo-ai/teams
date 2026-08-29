#!/usr/bin/env python3
"""Reserve a loopback TCP port without listening so model transport must fail closed."""

from __future__ import annotations

import argparse
import json
import os
from pathlib import Path
import signal
import socket
import time


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--host", required=True)
    parser.add_argument("--port", required=True, type=int)
    parser.add_argument("--ready-file", required=True)
    args = parser.parse_args()
    held = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    held.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 0)
    held.bind((args.host, args.port))
    probe = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    probe.settimeout(0.2)
    connected = probe.connect_ex((args.host, args.port)) == 0
    probe.close()
    if connected:
        raise RuntimeError("reserved transport-failure port unexpectedly accepts connections")
    ready = Path(args.ready_file)
    payload = json.dumps({
        "recordType": "SMA_S2_UNUSED_PORT_GUARD_READY",
        "pid": os.getpid(),
        "host": args.host,
        "port": args.port,
        "listening": False,
        "connectRefused": True,
    }, sort_keys=True, separators=(",", ":")).encode("utf-8")
    descriptor = os.open(ready, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, "wb") as stream:
        stream.write(payload + b"\n")
        stream.flush()
        os.fsync(stream.fileno())
    signal.signal(signal.SIGTERM, lambda *_args: (_ for _ in ()).throw(SystemExit(0)))
    while True:
        time.sleep(1)


if __name__ == "__main__":
    raise SystemExit(main())
