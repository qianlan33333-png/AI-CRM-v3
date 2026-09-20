#!/usr/bin/env python3
"""Require an immutable staging receipt for pull requests."""
from __future__ import annotations

import json
import os
import re
import subprocess
import sys


SHA = re.compile(r"^[0-9a-f]{40}$")


def main() -> int:
    if os.environ.get("GITHUB_EVENT_NAME") != "pull_request":
        return 0
    repo = os.environ["GITHUB_REPOSITORY"]
    number = os.environ["PR_NUMBER"]
    body = json.loads(subprocess.check_output(["gh", "api", f"repos/{repo}/pulls/{number}"], text=True))["body"] or ""
    current = os.environ.get("PR_HEAD_SHA") or subprocess.check_output(["git", "rev-parse", "HEAD"], text=True).strip()
    tree = subprocess.check_output(["git", "rev-parse", f"{current}^{{tree}}"], text=True).strip()
    values = {}
    for name in ("Staging-Head", "Staging-Tree", "Staging-Receipt"):
        match = re.search(rf"(?m)^{re.escape(name)}:\s*(\S+)\s*$", body)
        values[name] = match.group(1) if match else ""
    if not SHA.fullmatch(values["Staging-Head"]) or values["Staging-Head"] != current:
        raise SystemExit("staging receipt head does not match the current PR head")
    if not SHA.fullmatch(values["Staging-Tree"]) or values["Staging-Tree"] != tree:
        raise SystemExit("staging receipt tree does not match the current PR tree")
    if values["Staging-Receipt"].startswith("<"):
        raise SystemExit("staging receipt link is missing")
    print(json.dumps({"head": current, "tree": tree, "receipt": values["Staging-Receipt"]}, separators=(",", ":")))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
