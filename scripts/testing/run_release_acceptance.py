#!/usr/bin/env python3
"""Run one existing quality lane from a local-only, provider-disabled environment."""
from __future__ import annotations

import argparse
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import re
import subprocess
import sys
from typing import Any
from urllib.parse import parse_qs, unquote, urlparse
import uuid

HARNESS_ROOT = Path(__file__).resolve().parents[2]
LANES = ("preflight", "backend", "frontend", "browser", "archive-sdk")
LOCAL_HOSTS = {"127.0.0.1", "localhost", "::1"}
DATABASE_NAME = re.compile(r"aicrm_test_[A-Za-z0-9_]+\Z")
RUN_ID = re.compile(r"[a-z0-9][a-z0-9_-]{7,127}\Z")
SAFE_QUERY = {"sslmode": {"disable", "prefer", "require"}}
V2_DONOR_ALIASES = ("AICRM_V2_FROZEN_DONOR_DIR", "AICRM_V2_DONOR_ROOT", "PR04_DONOR_ROOT", "PR05_DONOR_ROOT", "PR07_DONOR_DIR", "PR08_DONOR_DIR", "PR09_DONOR_ROOT", "AICRM_PR06_DONOR_DIR", "AICRM_SURVEY_DONOR_DIR", "AICRM_AUTOMATIONOPS_DONOR_DIR", "AICRM_TX01_DONOR_DIR", "AICRM_CHANNEL_DONOR_DIR")
SIDEBAR_DONOR_ALIASES = ("AICRM_SIDEBAR_DONOR_DIR", "AICRM_SERVICE_PERIOD_MEMBER_GRID_DONOR_DIR")
DISABLED_PROVIDER_ENV = {"AICRM_WECOM_ENABLED": "false", "AICRM_OUTBOUND_PROVIDER_ENABLED": "false", "AICRM_WECOM_CALLBACK_ENABLED": "false", "AICRM_WECOM_TAG_CATALOG_PROVIDER_ENABLED": "false", "AICRM_WECOM_TAG_CATALOG_MUTATION_PROVIDER_ENABLED": "false", "AICRM_CHANNEL_PROVIDER_READ_ENABLED": "false", "AICRM_CHANNEL_QR_PROVIDER_ENABLED": "false", "AICRM_CHANNEL_MEDIA_PREP_PROVIDER_ENABLED": "false", "AICRM_CHANNEL_WELCOME_PROVIDER_ENABLED": "false", "AICRM_CHANNEL_TAG_PROVIDER_ENABLED": "false", "AICRM_CUSTOMER_TAG_PROVIDER_ENABLED": "false", "AICRM_GROUP_OPS_PROVIDER_ENABLED": "false", "AICRM_GROUP_OPS_PROVIDER_READ_ENABLED": "false", "AICRM_WECHAT_PAY_PROVIDER_ENABLED": "false", "AICRM_WECHAT_PAY_H5_OAUTH_ENABLED": "false", "AICRM_WECHAT_SHOP_PROVIDER_ENABLED": "false", "AICRM_SURVEY_COMPLETION_PROVIDER_ENABLED": "false", "AICRM_COMMERCE_PUSH_PROVIDER_ENABLED": "false", "AICRM_AUTOMATION_OPS_PROVIDER_MODE": "disabled", "AICRM_HXC_SYNC_ENABLED": "false", "AICRM_HXC_IDENTITY_WRITE_ENABLED": "false"}
TOOLCHAIN_ALLOWLIST = ("PATH", "LANG", "LC_ALL", "LC_CTYPE", "TZ", "TMPDIR", "GOCACHE", "GOMODCACHE", "GOPATH", "GOPROXY", "GOSUMDB", "GONOSUMDB", "GONOPROXY")

def utc_now() -> str:
    return datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")

def git(root: Path, *args: str) -> str:
    return subprocess.check_output(["git", *args], cwd=root, text=True).strip()

def git_state(root: Path) -> dict[str, Any]:
    return {"head": git(root, "rev-parse", "HEAD"), "tree": git(root, "rev-parse", "HEAD^{tree}"), "dirty": bool(git(root, "status", "--porcelain=v1", "--untracked-files=all"))}

def validated_database(url: str) -> tuple[str, dict[str, object]]:
    parsed = urlparse(url)
    if parsed.scheme not in {"postgres", "postgresql"} or parsed.hostname not in LOCAL_HOSTS:
        raise ValueError("test database must use a local postgres URL")
    try: port = 5432 if parsed.port is None else parsed.port
    except ValueError as error: raise ValueError("test database port is invalid") from error
    if not 1 <= port <= 65535: raise ValueError("test database port must be between 1 and 65535")
    database = unquote(parsed.path.lstrip("/"))
    if not DATABASE_NAME.fullmatch(database): raise ValueError("test database name must match aicrm_test_[A-Za-z0-9_]+")
    query = parse_qs(parsed.query, keep_blank_values=True, strict_parsing=True)
    if set(query) - set(SAFE_QUERY) or any(len(v) != 1 or v[0] not in SAFE_QUERY[k] for k, v in query.items()):
        raise ValueError("test database URL may only set sslmode=disable, prefer, or require")
    return url, {"host": parsed.hostname, "port": port, "database": database}

def outside_source(path: Path, source_root: Path) -> Path:
    resolved = path.resolve()
    try: resolved.relative_to(source_root)
    except ValueError: return resolved
    raise ValueError("report directory must be outside the checked-out source tree")

def require_clean(label: str, state: dict[str, Any]) -> None:
    if state["dirty"]: raise RuntimeError(label + " is dirty; create or use a clean worktree")

def integrity_violations(before: dict[str, Any], after: dict[str, Any], label: str) -> list[str]:
    checks = (("head_changed", before["head"] != after["head"]), ("tree_changed", before["tree"] != after["tree"]), ("dirty_after_run", after["dirty"]))
    return [label + "_" + name for name, changed in checks if changed]

def isolated_env(database_url: str, candidate_sha: str, dedup_base_sha: str, run_dir: Path, v2_donor_dir: Path | None, sidebar_donor_dir: Path | None) -> dict[str, str]:
    env = {key: os.environ[key] for key in TOOLCHAIN_ALLOWLIST if key in os.environ}
    safe_home = run_dir / "home"; safe_home.mkdir(parents=True, exist_ok=True)
    env.update({"HOME": str(safe_home), "PGPASSFILE": os.devnull, "PGSERVICEFILE": os.devnull, "PGSYSCONFDIR": str(safe_home / "pgconf"), "AICRM_DATABASE_URL": database_url, "AICRM_PUBLIC_ORIGIN": "https://release-acceptance.invalid", "AICRM_RELEASE_SHA": candidate_sha, "AICRM_DEDUP_HEAD_SHA": candidate_sha, "AICRM_DEDUP_BASE_SHA": dedup_base_sha, "PYTHONDONTWRITEBYTECODE": "1", **DISABLED_PROVIDER_ENV})
    v2, sidebar = (str(v2_donor_dir.resolve()) if v2_donor_dir else ""), (str(sidebar_donor_dir.resolve()) if sidebar_donor_dir else "")
    env.update({key: v2 for key in V2_DONOR_ALIASES}); env.update({key: sidebar for key in SIDEBAR_DONOR_ALIASES})
    return env

def redact(value: str) -> str:
    value = re.sub(r"(?i)(postgres(?:ql)?://[^\s/:@]+:)[^\s@/]+@", r"\1***@", value)
    return re.sub(r"(?i)((?:password|secret|token|api[_-]?key|private[_-]?key|cookie)\s*[=:]\s*)\S+", r"\1***", value)

def write_receipt(run_dir: Path, payload: dict[str, Any]) -> None:
    path = run_dir / "environment-receipt.json"
    if path.exists() and json.loads(path.read_text()).get("status") != "running": raise RuntimeError("completed receipt cannot be overwritten")
    path.write_text(json.dumps(payload, ensure_ascii=False, indent=2, sort_keys=True) + "\n")

def write_logs(run_dir: Path, stdout: str, stderr: str) -> None:
    (run_dir / "stdout.log").write_text(redact(stdout)); (run_dir / "stderr.log").write_text(redact(stderr))

def parser() -> argparse.ArgumentParser:
    value = argparse.ArgumentParser(description=__doc__)
    value.add_argument("lane", choices=LANES); value.add_argument("--test-database-url", default=os.environ.get("AICRM_TEST_DATABASE_URL")); value.add_argument("--report-dir", required=True, type=Path); value.add_argument("--run-id", default=uuid.uuid4().hex); value.add_argument("--source-root", type=Path, default=HARNESS_ROOT); value.add_argument("--v2-donor-dir", type=Path); value.add_argument("--sidebar-donor-dir", type=Path); value.add_argument("--candidate-sha", required=True); value.add_argument("--execute", action="store_true")
    return value

def main(argv: list[str] | None = None) -> int:
    args = parser().parse_args(argv)
    if not args.test_database_url: parser().error("--test-database-url or AICRM_TEST_DATABASE_URL is required")
    if not RUN_ID.fullmatch(args.run_id): parser().error("--run-id must contain only lowercase letters, digits, _ or -")
    source_root = args.source_root.resolve()
    if not (source_root / ".git").exists(): parser().error("--source-root must be a Git worktree")
    if len(args.candidate_sha) != 40 or any(char not in "0123456789abcdef" for char in args.candidate_sha): parser().error("--candidate-sha must be a lowercase 40-character SHA")
    run_dir = outside_source(args.report_dir, source_root) / args.run_id
    if run_dir.exists(): parser().error("run-id already exists; completed evidence is immutable")
    run_dir.mkdir(parents=True)
    receipt: dict[str, Any] = {"schema": 2, "status": "running", "run_id": args.run_id, "started_at_utc": utc_now(), "lane": args.lane, "mode": "execute" if args.execute else "prerequisites_only"}; stdout = stderr = ""
    try:
        harness_before, source_before = git_state(HARNESS_ROOT), git_state(source_root)
        receipt.update({"harness_before": harness_before, "source_before": source_before, "candidate_sha": source_before["head"], "harness_sha": harness_before["head"]}); require_clean("harness", harness_before); require_clean("source", source_before)
        if source_before["head"] != args.candidate_sha: raise RuntimeError("candidate SHA does not match the checked-out source")
        database_url, database = validated_database(args.test_database_url); dedup_base = git(source_root, "rev-parse", "HEAD^")
        command = [sys.executable, str(source_root / "scripts/ci/quality_lanes.py"), args.lane, "--report-dir", str(run_dir / "lane")]
        if not args.execute: command.append("--check-prerequisites")
        receipt.update({"database": database, "providers": "disabled", "provider_keys_forced_disabled": sorted(DISABLED_PROVIDER_ENV), "v2_donor_dir": str(args.v2_donor_dir.resolve()) if args.v2_donor_dir else None, "sidebar_donor_dir": str(args.sidebar_donor_dir.resolve()) if args.sidebar_donor_dir else None, "command": command}); write_receipt(run_dir, receipt)
        result = subprocess.run(command, cwd=source_root, env=isolated_env(database_url, source_before["head"], dedup_base, run_dir, args.v2_donor_dir, args.sidebar_donor_dir), capture_output=True, text=True, check=False); stdout, stderr = result.stdout, result.stderr; receipt["lane_exit_code"] = result.returncode
        harness_after, source_after = git_state(HARNESS_ROOT), git_state(source_root); receipt.update({"harness_after": harness_after, "source_after": source_after}); violations = integrity_violations(harness_before, harness_after, "harness") + integrity_violations(source_before, source_after, "source"); receipt["integrity_violations"] = violations; receipt["status"] = "success" if result.returncode == 0 and not violations else "failure"; receipt["exit_code"] = 3 if violations else result.returncode
    except Exception as error:
        receipt.update({"status": "failure", "exit_code": 2, "error": redact(str(error)), "error_type": type(error).__name__})
    finally:
        write_logs(run_dir, stdout, stderr); receipt["ended_at_utc"] = utc_now(); write_receipt(run_dir, receipt)
    if stdout: print(redact(stdout), end="")
    if stderr: print(redact(stderr), end="", file=sys.stderr)
    return int(receipt["exit_code"])

if __name__ == "__main__": raise SystemExit(main())
