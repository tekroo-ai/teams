#!/usr/bin/env python3
"""Hash-verifying launcher for the P2-M SMA-S1 evidence-v2 driver."""

from __future__ import annotations

import hashlib
import os
from pathlib import Path


SERVICE_JAR = Path(
    "/Users/paul/work/tekroo-ai/sma-s1-p2m/target/sma-1.0-SNAPSHOT.jar"
)
SERVICE_JAR_SHA256 = (
    "c0ddb963798d45603c2e2685744892c40d3e841817f9c966ab7fad34f7e48765"
)
WORKSPACE = Path(__file__).resolve().parent.parent
DRIVER_JAR = WORKSPACE / (
    "OUTPUT/phase-3/sma-s1-product-driver-p2m-evidence-v2-candidate-1/"
    "sma-s1-product-driver-p2m-evidence-v2-candidate-1.jar"
)
DRIVER_JAR_SHA256 = (
    "20956a708a29665d0ee52023fab925b65c7dea5f5beed7c1eab28036d8138eb3"
)
DRIVER_SOURCE = WORKSPACE / (
    "investigations/sma-q1/layered/driver-src-p2m-evidence-v2/"
    "ai/tekroo/sma/openhands/SmaS1QualificationDriver.java"
)
DRIVER_SOURCE_SHA256 = (
    "f748e28f91332609faf89d144df65de229bd5a9a0e239f2b7d595b85ae40b4b3"
)
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
            ["java", "-Dslf4j.internal.verbosity=ERROR", "-cp", classpath, MAIN_CLASS],
        )
    except (OSError, ValueError):
        return 2
    return 2


if __name__ == "__main__":
    raise SystemExit(main())
