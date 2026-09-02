#!/usr/bin/env python3
"""Minimal mechanical architecture checks for the clean v3 baseline."""

from __future__ import annotations

import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
MODULE_PREFIX = "github.com/qianlan33333-png/AI-CRM-v3/internal/"
DONOR_MARKERS = ("AI-CRM-production", "AI-CRM-v2", "aicrm_next")
IMPORT_RE = re.compile(r'"(' + re.escape(MODULE_PREFIX) + r'[^\"]+)"')


def fail(message: str) -> None:
    print(f"architecture: {message}", file=sys.stderr)
    raise SystemExit(1)


def domain_of(path: Path) -> str | None:
    parts = path.relative_to(ROOT).parts
    if len(parts) >= 2 and parts[0] == "internal":
        return parts[1]
    return None


def target_parts(import_path: str) -> tuple[str, tuple[str, ...]]:
    suffix = import_path.removeprefix(MODULE_PREFIX)
    parts = tuple(suffix.split("/"))
    return parts[0], parts[1:]


def main() -> None:
    go_files = sorted((ROOT / "cmd").rglob("*.go")) + sorted((ROOT / "internal").rglob("*.go"))
    if not go_files:
        fail("no Go source files found")

    for path in go_files:
        source = path.read_text(encoding="utf-8")
        for marker in DONOR_MARKERS:
            if marker in source:
                fail(f"{path.relative_to(ROOT)} references donor marker {marker}")

        owner = domain_of(path)
        if owner != "config" and "/platform/config/" not in f"/{path.relative_to(ROOT).as_posix()}/":
            if "os.Getenv(" in source or "os.LookupEnv(" in source:
                fail(f"{path.relative_to(ROOT)} reads environment outside platform/config")

        for imported in IMPORT_RE.findall(source):
            target, remainder = target_parts(imported)
            if owner is None:  # cmd is the composition root.
                continue
            if owner == "platform":
                if target != "platform":
                    fail(f"platform imports business domain {target}: {path.relative_to(ROOT)}")
                continue
            if target in (owner, "platform"):
                continue
            if not remainder or remainder[0] not in ("port", "domain"):
                fail(
                    f"cross-domain concrete import {imported} from {path.relative_to(ROOT)}; "
                    "only port/domain contracts are allowed"
                )

    print(f"architecture: checked {len(go_files)} Go files")


if __name__ == "__main__":
    main()
