#!/usr/bin/env python3
from __future__ import annotations

import hashlib
import importlib.util
from pathlib import Path


RUN_DIR = Path(__file__).resolve().parent
BASE_DRIVER = RUN_DIR.parent / "sma-s2-final-p2-measured-run-1/run_measured.py"
PREPARATION = RUN_DIR.parent / "sma-s2-final-p2-measured-execution-preparation-2"
BASE_DRIVER_SHA256 = "1828eed6056280ae7385853e85b89c87057b86a49fb866f7829a13c25d82813a"
EXECUTION_IDENTITY = "23262b987196522ad7c420546c982e95901f0e9f65c5ae914606563ec3bfd069"


def main() -> int:
    assert hashlib.sha256(BASE_DRIVER.read_bytes()).hexdigest() == BASE_DRIVER_SHA256
    specification = importlib.util.spec_from_file_location("sma_s2_measured_base_driver", BASE_DRIVER)
    assert specification is not None and specification.loader is not None
    module = importlib.util.module_from_spec(specification)
    specification.loader.exec_module(module)
    module.RUN_DIR = RUN_DIR
    module.PREPARATION = PREPARATION
    module.EXECUTION_IDENTITY = EXECUTION_IDENTITY
    return module.main()


if __name__ == "__main__":
    raise SystemExit(main())
