#!/usr/bin/env python3
"""Hash-verifying launcher for the model-free SMA-S1 product driver candidate."""

from __future__ import annotations

import hashlib
import os
from pathlib import Path


SERVICE_JAR = Path("/Users/paul/work/tekroo-ai/sma/target/sma-1.0-SNAPSHOT.jar")
SERVICE_JAR_SHA256 = "ead8c0c5d9e3fd37b61318d2bdb8a97ef5c4c7481c11ed453777492f39f71042"
WORKSPACE = Path(__file__).resolve().parent.parent
DRIVER_JAR = WORKSPACE / "OUTPUT/phase-3/sma-s1-product-driver-candidate-4/sma-s1-product-driver-candidate-4.jar"
DRIVER_JAR_SHA256 = "e5d0cd194d77968dd18bf2aacd9dbbeb7eea68632b58baabc63681d99881e63a"
DRIVER_SOURCE = WORKSPACE / "investigations/sma-q1/layered/driver-src/ai/tekroo/sma/openhands/SmaS1QualificationDriver.java"
DRIVER_SOURCE_SHA256 = "b22f2e658fad9aa38317e8647f070984a03f7a11a2d1ab0a6a4771243d213345"
MAIN_CLASS = "ai.tekroo.sma.openhands.SmaS1QualificationDriver"


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def main() -> int:
    try:
        bindings = (
            (SERVICE_JAR, SERVICE_JAR_SHA256),
            (DRIVER_JAR, DRIVER_JAR_SHA256),
            (DRIVER_SOURCE, DRIVER_SOURCE_SHA256),
        )
        if any(not path.is_file() or sha256_file(path) != expected for path, expected in bindings):
            return 2
        classpath = os.pathsep.join((str(DRIVER_JAR), str(SERVICE_JAR)))
        os.execvp(
            "java",
            ["java", "-Dslf4j.internal.verbosity=ERROR", "-cp", classpath, MAIN_CLASS],
        )
    except (OSError, ValueError):
        return 2
    return 2


if __name__ == "__main__":
    raise SystemExit(main())
