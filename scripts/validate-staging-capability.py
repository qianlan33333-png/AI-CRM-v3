#!/usr/bin/env python3
"""Validate only the declared staging capability and classify unrelated outages."""
from __future__ import annotations

import argparse
import json
from pathlib import Path
from typing import Any


def load(path: Path) -> dict[str, Any]:
    value = json.loads(path.read_text())
    if not isinstance(value, dict):
        raise ValueError(f"{path} must contain an object")
    return value


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("capability", type=Path)
    parser.add_argument("readback", type=Path)
    args = parser.parse_args()
    capability = load(args.capability)
    readback = load(args.readback)
    required = set(capability.get("required_provider_dependencies", []))
    routes = capability.get("required_routes", [])
    if not isinstance(required, set) or not all(isinstance(x, str) for x in required):
        raise SystemExit("required_provider_dependencies must be a string array")
    if not isinstance(routes, list):
        raise SystemExit("required_routes must be an array")
    observations = readback.get("observations", [])
    if not isinstance(observations, list):
        raise SystemExit("readback observations must be an array")

    unrelated: list[dict[str, Any]] = []
    for item in observations:
        if not isinstance(item, dict):
            raise SystemExit("each readback observation must be an object")
        status = item.get("status")
        provider = item.get("provider")
        error = item.get("error")
        if status in (500, 502, 503, 504) and error == "distribution_unavailable":
            if provider in required or "distribution" in required:
                raise SystemExit("required Distribution provider is unavailable")
            unrelated.append(item)
            continue
        if item.get("required") is True and status not in range(200, 300):
            raise SystemExit(f"required route failed: {item.get('route', '<unknown>')} status={status}")

    result = {
        "classification": "external_config_unavailable" if unrelated else "accepted",
        "required_provider_dependencies": sorted(required),
        "unrelated_observations": unrelated,
        "business_readback": readback.get("business_readback", ""),
    }
    print(json.dumps(result, ensure_ascii=False, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
