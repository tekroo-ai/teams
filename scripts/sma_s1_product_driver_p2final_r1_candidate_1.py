#!/usr/bin/env python3
"""Hash-verifying launcher for the final-P2 SMA-S1 R1 candidate 1."""

from __future__ import annotations

import hashlib
import os
from pathlib import Path


SERVICE_JAR = Path(
    "/Users/paul/work/tekroo-ai/sma-step15-s1-p2final/target/sma-1.0-SNAPSHOT.jar"
)
SERVICE_JAR_SHA256 = "1561d18739cc53e744a0014ce562c8a7b2adfc988494a984728d680e887fbb8b"
WORKSPACE = Path(__file__).resolve().parent.parent
DRIVER_JAR = WORKSPACE / (
    "OUTPUT/phase-3/sma-s1-product-driver-p2final-r1-candidate-1/"
    "sma-s1-product-driver-p2final-r1-candidate-1.jar"
)
DRIVER_JAR_SHA256 = "00ac8098562215b9c021b2512776457d6b123a731c650cc9224d5f8bcff67e8f"
DRIVER_SOURCE = WORKSPACE / (
    "investigations/sma-q1/layered/driver-src-p2m-evidence-v2-candidate-4/"
    "ai/tekroo/sma/openhands/SmaS1QualificationDriver.java"
)
DRIVER_SOURCE_SHA256 = "184fa7bc9bc468b55a9c94d134841161938b9572a7e359265620d84358ed7667"
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
        if any(
            not path.is_file() or sha256_file(path) != expected
            for path, expected in bindings
        ):
            return 2
        classpath = os.pathsep.join((str(DRIVER_JAR), str(SERVICE_JAR)))
        os.execvp(
            "java",
            [
                "java",
                "-Dslf4j.internal.verbosity=ERROR",
                "-cp",
                classpath,
                MAIN_CLASS,
            ],
        )
    except (OSError, ValueError):
        return 2
    return 2


if __name__ == "__main__":
    raise SystemExit(main())
