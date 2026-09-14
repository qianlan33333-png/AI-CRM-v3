#!/usr/bin/env python3
"""Run one existing quality lane with an isolated, provider-disabled test environment.

This wrapper does not replace ``scripts/ci/quality_lanes.py``.  It rejects a
non-local or non-``aicrm_test_*`` database before delegating to that canonical
lane, removes inherited AICRM settings, and writes a sanitized receipt outside
the checked-out source tree.
"""
from __future__ import annotations

import argparse
import json
import os
from pathlib import Path
import subprocess
import sys
from urllib.parse import unquote, urlparse


HARNESS_ROOT = Path(__file__).resolve().parents[2]
LANES = ("preflight", "backend", "frontend", "browser", "archive-sdk")
LOCAL_HOSTS = {"127.0.0.1", "localhost", "::1"}
DISABLED_PROVIDER_ENV = {
    "AICRM_WECOM_ENABLED": "false",
    "AICRM_OUTBOUND_PROVIDER_ENABLED": "false",
    "AICRM_WECOM_CALLBACK_ENABLED": "false",
    "AICRM_WECOM_TAG_CATALOG_PROVIDER_ENABLED": "false",
    "AICRM_WECOM_TAG_CATALOG_MUTATION_PROVIDER_ENABLED": "false",
    "AICRM_CHANNEL_PROVIDER_READ_ENABLED": "false",
    "AICRM_CHANNEL_QR_PROVIDER_ENABLED": "false",
    "AICRM_CHANNEL_MEDIA_PREP_PROVIDER_ENABLED": "false",
    "AICRM_CHANNEL_WELCOME_PROVIDER_ENABLED": "false",
    "AICRM_CHANNEL_TAG_PROVIDER_ENABLED": "false",
    "AICRM_CUSTOMER_TAG_PROVIDER_ENABLED": "false",
    "AICRM_GROUP_OPS_PROVIDER_ENABLED": "false",
    "AICRM_GROUP_OPS_PROVIDER_READ_ENABLED": "false",
    "AICRM_WECHAT_PAY_PROVIDER_ENABLED": "false",
    "AICRM_WECHAT_PAY_H5_OAUTH_ENABLED": "false",
    "AICRM_WECHAT_SHOP_PROVIDER_ENABLED": "false",
    "AICRM_SURVEY_COMPLETION_PROVIDER_ENABLED": "false",
    "AICRM_COMMERCE_PUSH_PROVIDER_ENABLED": "false",
    "AICRM_AUTOMATION_OPS_PROVIDER_MODE": "disabled",
    "AICRM_HXC_SYNC_ENABLED": "false",
    "AICRM_HXC_IDENTITY_WRITE_ENABLED": "false",
}


def git(root: Path, *args: str) -> str:
    return subprocess.check_output(["git", *args], cwd=root, text=True).strip()


def validated_database(url: str) -> tuple[str, dict[str, object]]:
    parsed = urlparse(url)
    database = unquote(parsed.path.strip("/"))
    if parsed.scheme not in {"postgres", "postgresql"}:
        raise ValueError("test database URL must use postgres or postgresql")
    if parsed.hostname not in LOCAL_HOSTS:
        raise ValueError("test database host must be localhost, 127.0.0.1, or ::1")
    if not database.startswith("aicrm_test_"):
        raise ValueError("test database name must start with aicrm_test_")
    return url, {"host": parsed.hostname, "port": parsed.port or 5432, "database": database}


def outside_source(path: Path, source_root: Path) -> Path:
    resolved = path.resolve()
    try:
        resolved.relative_to(source_root)
    except ValueError:
        return resolved
    raise ValueError("report directory must be outside the checked-out source tree")


def require_clean_source(source_root: Path) -> None:
    if git(source_root, "status", "--porcelain=v1", "--untracked-files=all"):
        raise RuntimeError("source tree is dirty; commit the harness or create a fresh test worktree before running")


def isolated_env(database_url: str, candidate_sha: str) -> dict[str, str]:
    env = dict(os.environ)
    # A test run must not inherit credentials, runtime files, or a provider gate
    # from the caller.  Non-AICRM toolchain configuration remains available.
    for key in tuple(env):
        if key.startswith("AICRM_"):
            del env[key]
    env.update(DISABLED_PROVIDER_ENV)
    env.update({
        "AICRM_DATABASE_URL": database_url,
        "AICRM_PUBLIC_ORIGIN": "https://release-acceptance.invalid",
        "AICRM_RELEASE_SHA": candidate_sha,
    })
    return env


def write_receipt(report_dir: Path, payload: dict[str, object]) -> None:
    report_dir.mkdir(parents=True, exist_ok=True)
    (report_dir / "environment-receipt.json").write_text(
        json.dumps(payload, ensure_ascii=False, indent=2, sort_keys=True) + "\n"
    )


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("lane", choices=LANES)
    parser.add_argument("--test-database-url", default=os.environ.get("AICRM_TEST_DATABASE_URL"))
    parser.add_argument("--report-dir", required=True, type=Path)
    parser.add_argument("--source-root", type=Path, default=HARNESS_ROOT,
                        help="clean candidate worktree to test; defaults to this harness checkout")
    parser.add_argument("--candidate-sha", required=True)
    parser.add_argument("--execute", action="store_true", help="run the lane; otherwise only check its prerequisites")
    args = parser.parse_args()
    if not args.test_database_url:
        parser.error("--test-database-url or AICRM_TEST_DATABASE_URL is required")
    if len(args.candidate_sha) != 40 or any(char not in "0123456789abcdef" for char in args.candidate_sha):
        parser.error("--candidate-sha must be a lowercase 40-character SHA")
    source_root = args.source_root.resolve()
    if not (source_root / ".git").exists():
        parser.error("--source-root must be a Git worktree")
    current = git(source_root, "rev-parse", "HEAD")
    if current != args.candidate_sha:
        parser.error("candidate SHA does not match the checked-out source")
    require_clean_source(source_root)
    database_url, database = validated_database(args.test_database_url)
    report_dir = outside_source(args.report_dir, source_root)
    command = [sys.executable, str(source_root / "scripts/ci/quality_lanes.py"), args.lane,
               "--report-dir", str(report_dir / "lane")]
    if not args.execute:
        command.append("--check-prerequisites")
    receipt = {
        "schema": 1,
        "candidate_sha": current,
        "harness_sha": git(HARNESS_ROOT, "rev-parse", "HEAD"),
        "lane": args.lane,
        "mode": "execute" if args.execute else "prerequisites_only",
        "database": database,
        "providers": "disabled",
        "provider_keys_forced_disabled": sorted(DISABLED_PROVIDER_ENV),
        "command": ["scripts/ci/quality_lanes.py", args.lane, "--check-prerequisites" if not args.execute else "execute"],
    }
    write_receipt(report_dir, receipt)
    result = subprocess.run(command, cwd=source_root, env=isolated_env(database_url, current), check=False)
    receipt["exit_code"] = result.returncode
    write_receipt(report_dir, receipt)
    return result.returncode


if __name__ == "__main__":
    raise SystemExit(main())
