#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)"

exec python3 - "$REPO_ROOT" <<'PY'
from __future__ import annotations

import json
import re
import subprocess
import sys
from pathlib import Path


root = Path(sys.argv[1]).resolve()
manifest_path = root / "docs/migration/config-definitions/expected-manifest.json"
contract_path = root / "docs/migration/config-definitions/source-contract.md"
runbook_path = root / "docs/migration/config-definitions/production-import-runbook.md"
failures: list[str] = []


def fail(message: str) -> None:
    failures.append(message)


def read_json(path: Path):
    try:
        return json.loads(path.read_text(encoding="utf-8"))
    except FileNotFoundError:
        fail(f"missing manifest: {path.relative_to(root)}")
    except (OSError, json.JSONDecodeError) as exc:
        fail(f"invalid JSON {path.relative_to(root)}: {exc}")
    return None


manifest = read_json(manifest_path)
if isinstance(manifest, dict):
    expected_scope = {
        "products": {"total": 31, "ordinary": 29, "service_period": 2},
        "coupons": {"definitions": 15, "bindings": 15},
        "group_ops": {"plans": 12, "references": 14, "text_nodes": 3},
        "automation": {"runtime_configs": 10},
    }
    if manifest.get("manifest_version") != "config-definition-import.v1":
        fail("manifest_version must be config-definition-import.v1")
    if manifest.get("migration_id") != "config-definitions":
        fail("migration_id must be config-definitions")
    if manifest.get("source_system") != "AI-CRM-production:150.158.82.186/openclaw_wecom":
        fail("source_system must identify the approved production source database")
    if manifest.get("schema_digest_algorithm") != "sha256(canonical-json-utf8)":
        fail("schema_digest_algorithm must be sha256(canonical-json-utf8)")
    if manifest.get("scope") != expected_scope:
        fail(f"scope mismatch: expected {expected_scope!r}")

    architecture = manifest.get("architecture")
    expected_architecture = {
        "oneid": "not_involved",
        "external_effects": "not_involved",
        "transaction": "local_postgresql_migration_transaction",
        "provider_network_calls": False,
        "customer_root_resolution": False,
    }
    if architecture != expected_architecture:
        fail("architecture must declare OneID/effects not involved and a local PostgreSQL transaction")

    frontend = manifest.get("frontend_boundary")
    if not isinstance(frontend, dict) or frontend.get("donor_web_files_must_be_unchanged") is not True:
        fail("frontend_boundary must freeze donor web files")
    if not isinstance(frontend, dict) or frontend.get("checked_roots") != ["web/donors"]:
        fail("frontend_boundary.checked_roots must be exactly web/donors")

    expected_targets = {
        "products",
        "product_imported_service_period_definitions",
        "coupon_rules",
        "coupon_rule_targets",
        "group_ops_plans",
        "group_ops_plan_group_assets",
        "group_ops_plan_nodes",
        "automation_agents",
    }
    if set(manifest.get("target_tables", [])) != expected_targets:
        fail("target_tables must contain only the approved configuration-definition owner tables")

    required_exclusions = {
        "tags",
        "materials",
        "customer_roots",
        "history",
        "messages",
        "execution",
        "external_effects",
        "webhook_urls",
        "webhook_secrets",
        "claims",
        "redemptions",
    }
    exclusions = set(manifest.get("excluded_scopes", []))
    if not required_exclusions.issubset(exclusions):
        fail(f"excluded_scopes is missing {sorted(required_exclusions - exclusions)}")

    required_forbidden_tokens = {
        "tag",
        "material",
        "customer",
        "history",
        "message",
        "external_effect",
        "webhook",
        "secret",
    }
    forbidden_tokens = set(manifest.get("forbidden_source_field_tokens", []))
    if not required_forbidden_tokens.issubset(forbidden_tokens):
        fail(f"forbidden_source_field_tokens is missing {sorted(required_forbidden_tokens - forbidden_tokens)}")

    required_excluded_tables = {
        "commerce_coupon_claims",
        "commerce_coupon_redemptions",
        "coupon_claims",
        "coupon_redemptions",
    }
    excluded_tables = set(manifest.get("excluded_source_tables", []))
    if not required_excluded_tables.issubset(excluded_tables):
        fail(f"excluded_source_tables is missing {sorted(required_excluded_tables - excluded_tables)}")

    snapshot_policy = manifest.get("source_snapshot")
    if not isinstance(snapshot_policy, dict):
        fail("source_snapshot policy is missing")
    else:
        if snapshot_policy.get("same_snapshot_replay") != "idempotent":
            fail("same snapshot replay must be idempotent")
        if snapshot_policy.get("different_snapshot_same_batch_key") != "reject_as_drift":
            fail("different snapshot under the same batch key must reject as drift")

for document in (contract_path, runbook_path):
    try:
        text = document.read_text(encoding="utf-8")
    except FileNotFoundError:
        fail(f"missing contract document: {document.relative_to(root)}")
        continue
    if "OneID" not in text:
        fail(f"{document.relative_to(root)} must declare OneID classification")
    if not re.search(r"external effects|External Effects", text, re.IGNORECASE):
        fail(f"{document.relative_to(root)} must declare External Effects classification")


def git_lines(*args: str) -> list[str]:
    try:
        result = subprocess.run(
            ["git", *args],
            cwd=root,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            check=False,
        )
    except OSError as exc:
        fail(f"cannot inspect git diff: {exc}")
        return []
    if result.returncode != 0:
        fail(f"git {' '.join(args)} failed: {result.stderr.strip()}")
        return []
    return [line.strip() for line in result.stdout.splitlines() if line.strip()]


changed = set(git_lines("diff", "--name-only", "HEAD", "--"))
changed.update(git_lines("ls-files", "--others", "--exclude-standard"))
donor_changes = sorted(path for path in changed if path == "web/donors" or path.startswith("web/donors/"))
if donor_changes:
    fail("donor web business files changed: " + ", ".join(donor_changes))


def code_files() -> list[Path]:
    found: set[Path] = set()
    migration_root = root / "internal/configmigration"
    if migration_root.exists():
        for path in migration_root.rglob("*"):
            if path.is_file() and path.suffix.lower() in {".go", ".sql", ".py", ".sh"} and not path.name.endswith("_test.go"):
                found.add(path)

    command_root = root / "cmd"
    if command_root.exists():
        for path in command_root.glob("migrate-*"):
            if path.is_dir() and ("config" in path.name.lower() or "definition" in path.name.lower()):
                for child in path.rglob("*"):
                    if child.is_file() and child.suffix.lower() in {".go", ".sql", ".py", ".sh"}:
                        found.add(child)

    # Only newly changed migration SQL is in scope. Historical migration files
    # are not a source snapshot and may legitimately contain excluded domains.
    for relative in changed:
        if relative.startswith("migrations/") and Path(relative).suffix.lower() == ".sql":
            path = root / relative
            if path.is_file():
                found.add(path)
    return sorted(found)


def strip_comments(text: str) -> str:
    # Keep quoted SQL/Go strings: field and table references inside queries
    # must be checked. Remove comments so the contract examples in source
    # comments do not become false positives.
    pattern = re.compile(r"/\*.*?\*/|//[^\n]*|--[^\n]*|^[ \t]*#[^\n]*", re.MULTILINE | re.DOTALL)
    return pattern.sub(lambda match: "\n" * match.group(0).count("\n"), text)


# These are field/table names, rather than broad English words. In particular,
# claim_starts_at/claim_ends_at and message_text/text_content are definition
# fields and are intentionally allowed.
forbidden_patterns = [
    r"\b(?:wecom_)?tag(?:_id|_ids|_group|_group_id|_group_ids|ging)\b",
    r"\b(?:label|labels)_(?:id|ids|assignment|assignments)\b",
    r"\b(?:media|material|attachment|image|mini_program|miniprogram)_(?:id|ids|reference|references|library_id|library_ids)\b",
    r"\b(?:material_reference|material_plan|fixed_content_(?:package_id|asset_id)|content_package_(?:id|ids))\b",
    r"\b(?:customer_id|customer_ids|customer_uuid|customer_root_id|customer_root_ids|identity_id|identity_ids|openid|openids|unionid|unionids|external_userid|external_userids|audience_id|audience_ids|recipient_id|recipient_ids|owner_userid)\b",
    r"\b(?:phone|phone_number|phone_numbers|mobile_number|mobile_numbers|email|email_address|email_addresses)\b",
    r"\b(?:history|historical|history_id|history_ids|message_id|message_ids|message_history|message_log|message_record|message_records|execution|execution_id|execution_ids|external_effect|external_effect_id|effect_id|effect_ids|provider_receipt|provider_receipt_id|receipt_id|outbox|outbox_id|reconciliation|reconciliation_id)\b",
    r"\bwebhook_(?:url|secret|token|endpoint|path|signature|signing_secret)\b",
    r"\b(?:secret|secrets|api_key|apikey|password|credential|credentials|access_token|refresh_token)\b",
    r"\b(?:commerce_coupon_claims?|commerce_coupon_redemptions?|coupon_claims?|coupon_redemptions?|redemptions?)\b",
]
compiled_forbidden = [(re.compile(pattern, re.IGNORECASE), pattern) for pattern in forbidden_patterns]
forbidden_imports = re.compile(
    r"internal/(?:tag|media|customer|identity|externaleffects|outbound)(?:/|[\"])",
    re.IGNORECASE,
)

migration_files = code_files()
migration_text = ""
# PRD07 §14 authorizes a separate, explicitly named history mode in this one
# command. Its source fields must remain confined to these files; the ordinary
# definition mode and its immutable allowlist stay under the checks above.
history_mode_files = {
    "cmd/migrate-v2-config-definitions/main.go",
    "cmd/migrate-v2-config-definitions/integration_test.go",
    "internal/configmigration/source/history.go",
    "internal/configmigration/source/history_test.go",
    "internal/configmigration/target/groupops_history.go",
    "internal/configmigration/target/groupops_history_test.go",
}
history_mode_tokens = {"history", "historical", "owner_userid"}
for path in migration_files:
    try:
        raw = path.read_text(encoding="utf-8")
    except (OSError, UnicodeDecodeError) as exc:
        fail(f"cannot read migration code {path.relative_to(root)}: {exc}")
        continue
    clean = strip_comments(raw)
    migration_text += "\n" + clean.lower()
    relative = path.relative_to(root)
    for regex, pattern in compiled_forbidden:
        match = regex.search(clean)
        if match:
            if relative.as_posix() in history_mode_files and match.group(0).lower() in history_mode_tokens:
                continue
            line = clean.count("\n", 0, match.start()) + 1
            fail(f"forbidden source field/table token {match.group(0)!r} in {relative}:{line}")
    if forbidden_imports.search(clean):
        fail(f"migration code imports an excluded domain in {relative}")

required_source_tables = [
    "wechat_pay_products",
    "service_period_products",
    "commerce_coupons",
    "commerce_coupon_product_bindings",
    "automation_group_ops_plans",
    "automation_group_ops_plan_nodes",
    "automation_group_ops_plan_groups",
    "automation_agent_runtime_config",
]
if migration_files:
    for table in required_source_tables:
        if table not in migration_text:
            fail(f"migration source allowlist is missing table {table}")
    for field in ("claim_starts_at", "claim_ends_at"):
        if field not in migration_text:
            fail(f"coupon rule-definition field {field} is missing from the source allowlist")

baseline_path = root / "internal/configmigration/source/snapshot.go"
if baseline_path.is_file():
    try:
        baseline = baseline_path.read_text(encoding="utf-8")
    except (OSError, UnicodeDecodeError) as exc:
        fail(f"cannot read expected baseline: {exc}")
    else:
        expected_baseline = {
            "products": 31,
            "service_periods": 2,
            "coupons": 15,
            "coupon_bindings": 15,
            "group_plans": 12,
            "group_nodes": 3,
            "group_assets": 14,
            "agents": 10,
        }
        for name, count in expected_baseline.items():
            if not re.search(rf'"{re.escape(name)}"\s*:\s*{count}\b', baseline):
                fail(f"source baseline does not freeze {name}={count}")

if failures:
    print("FAIL config-definition import boundary", file=sys.stderr)
    for message in failures:
        print(f"- {message}", file=sys.stderr)
    raise SystemExit(1)

print("PASS config-definition import boundary")
print("counts: products=31 (ordinary=29, service_period=2), coupons=15, bindings=15, group_plans=12, group_references=14, group_text_nodes=3, agent_runtime_configs=10")
print("architecture: OneID=not_involved, External Effects=not_involved, transaction=local_postgresql_migration_transaction")
print("frontend: web/donors byte-frozen; claims/redemptions and forbidden source fields excluded")
PY
