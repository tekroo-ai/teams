#!/usr/bin/env python3
from __future__ import annotations

import hashlib
import importlib.util
from pathlib import Path


RUN_DIR = Path(__file__).resolve().parent
BASE_DRIVER = RUN_DIR.parent / "sma-s2-final-p2-measured-run-1/run_measured.py"
BASE_DRIVER_SHA256 = "1828eed6056280ae7385853e85b89c87057b86a49fb866f7829a13c25d82813a"
EXECUTION_IDENTITY = "5d0bb7436a0b4cf24fa4e5a672caa69a7505c4abc0ca4d40b95d388aa84e6d43"
PACKAGE_IDENTITY = "5b56a52511a315c7418e1476fe912b9d1f25232eda5e9b1f902ef3de361a3467"
PLAN_SHA256 = "4c0ffad0996747cfa7002694461048e7b7ea3aae2d3731757efdb7b150024fa6"


def main() -> int:
    assert hashlib.sha256(BASE_DRIVER.read_bytes()).hexdigest() == BASE_DRIVER_SHA256
    specification = importlib.util.spec_from_file_location("sma_s2_measured_base_driver", BASE_DRIVER)
    assert specification is not None and specification.loader is not None
    module = importlib.util.module_from_spec(specification)
    specification.loader.exec_module(module)
    module.RUN_DIR = RUN_DIR
    module.PREPARATION = RUN_DIR
    module.EXECUTION_IDENTITY = EXECUTION_IDENTITY
    module.PACKAGE_IDENTITY = PACKAGE_IDENTITY
    module.PLAN_SHA256 = PLAN_SHA256
    return module.main()


if __name__ == "__main__":
    raise SystemExit(main())
