#!/usr/bin/env python3
"""Build a release-acceptance coverage inventory from source evidence.

This is deliberately an inventory tool, not a test runner.  It reports the
documented OpenAPI surface, source-level mount/route evidence and test-file
references.  Existing tests are evidence that a mapping exists; they are not
reported as passing unless a run receipt is supplied separately.
"""

from __future__ import annotations

import argparse
import csv
import json
import re
import subprocess
from collections import Counter, defaultdict
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path
from typing import Any, Iterable


HTTP_METHODS = {"get", "post", "put", "patch", "delete", "head", "options", "trace"}
TEST_SUFFIXES = ("_test.go", ".test.mjs", ".test.ts", ".test.js", ".test.py", ".test.sh")


@dataclass(frozen=True)
class SourceLine:
    path: str
    line: int
    text: str


def rel(root: Path, path: Path) -> str:
    return path.relative_to(root).as_posix()


def parse_openapi(root: Path) -> list[dict[str, Any]]:
    """Use Ruby's stdlib YAML parser; this repo does not require PyYAML."""

    script = (
        "require 'yaml'; require 'json'; "
        "d=YAML.load_file(ARGV.fetch(0)); puts JSON.generate(d)"
    )
    completed = subprocess.run(
        ["ruby", "-e", script, str(root / "api/openapi.yaml")],
        check=True,
        text=True,
        capture_output=True,
    )
    document = json.loads(completed.stdout)
    operations: list[dict[str, Any]] = []
    for path, path_item in document.get("paths", {}).items():
        if not isinstance(path_item, dict):
            continue
        for method, operation in path_item.items():
            if method.lower() not in HTTP_METHODS or not isinstance(operation, dict):
                continue
            security = operation.get("security") or []
            security_names = sorted({name for item in security if isinstance(item, dict) for name in item})
            responses = operation.get("responses") or {}
            operations.append(
                {
                    "method": method.upper(),
                    "path": path,
                    "operation_id": operation.get("operationId", ""),
                    "tags": operation.get("tags") or [],
                    "security": security_names,
                    "response_codes": sorted(str(code) for code in responses),
                    "has_request_body": bool(operation.get("requestBody")),
                    "parameters": [
                        p.get("name")
                        for p in operation.get("parameters", [])
                        if isinstance(p, dict) and p.get("name")
                    ],
                }
            )
    return operations


def iter_source_lines(root: Path) -> list[SourceLine]:
    lines: list[SourceLine] = []
    excluded = {".git", "web/donors", "web/donor-sources", "web/dist", "node_modules"}
    for path in root.rglob("*"):
        if not path.is_file() or any(part in excluded for part in path.parts) or "web/donors" in str(path) or "web/donor-sources" in str(path):
            continue
        if path.suffix not in {".go", ".ts", ".js", ".mjs", ".py", ".sh", ".yaml", ".md"}:
            continue
        try:
            content = path.read_text(encoding="utf-8", errors="replace")
        except OSError:
            continue
        lines.extend(SourceLine(rel(root, path), number, line) for number, line in enumerate(content.splitlines(), 1))
    return lines


def test_files(root: Path) -> list[tuple[str, str]]:
    files: list[tuple[str, str]] = []
    for path in root.rglob("*"):
        if not path.is_file() or not path.name.endswith(TEST_SUFFIXES):
            continue
        if any(part in {".git", "web", "node_modules"} for part in path.parts) and ("web/donors" in str(path) or "web/donor-sources" in str(path) or "web/dist" in str(path) or "node_modules" in str(path)):
            continue
        try:
            files.append((rel(root, path), path.read_text(encoding="utf-8", errors="replace")))
        except OSError:
            pass
    return files


def path_prefix(path: str) -> str:
    # The stable literal prefix is enough to prove a route family is exercised;
    # variable values are intentionally not invented by this inventory.
    before_variable = path.split("{", 1)[0]
    if before_variable != path:
        return before_variable.rstrip("/") + "/"
    return path


def module_for(operation: dict[str, Any]) -> str:
    tags = operation.get("tags") or []
    if tags:
        tag = str(tags[0])
        return {
            "AIAssistant": "ai-assistant",
            "Automation": "automation",
            "AutomationOperations": "automation-operations",
            "Channels": "channel-acquisition",
            "Config": "config",
            "CouponRules": "coupons",
            "Customer": "customer",
            "CustomerTags": "customer-tags",
            "GroupOps": "group-operations",
            "HXCDashboard": "hxc-dashboard",
            "Media": "media",
            "MessageArchive": "message-archive",
            "OpenPlatform": "open-platform",
            "OperationCycles": "operation-cycles",
            "Products": "products",
            "Radar": "radar",
            "Sidebar": "sidebar",
            "Transactions": "transactions",
            "WeCom": "wecom",
            "WeComTags": "wecom-tags",
        }.get(tag, tag.lower())
    path = operation["path"]
    op_id = operation.get("operation_id", "")
    if path in {"/healthz", "/readyz"}:
        return "platform-health"
    if path in {"/login", "/logout"} or path.startswith("/api/admin/access") or "Access" in op_id or "Login" in op_id:
        return "access"
    if path.startswith("/auth/wecom") or path.startswith("/api/wecom") or path.startswith("/wecom/"):
        return "wecom"
    if path.startswith("/api/admin/oneid") or "OneID" in op_id:
        return "oneid"
    if path.startswith("/api/v1/distribution") or path.startswith("/api/admin/distribution") or path.startswith("/d/") or "Distribution" in op_id:
        return "distribution"
    if path.startswith("/api/admin/external-effects") or path.startswith("/api/admin/push-center"):
        return "external-effects"
    if path.startswith("/api/admin/customers") or path.startswith("/api/v1/customers") or path.startswith("/admin/customers"):
        return "customer"
    if path.startswith("/api/admin/customer-sync"):
        return "customer-sync"
    if path.startswith("/api/h5/") or path.startswith("/api/public/") or path.startswith("/q/") or path.startswith("/r/"):
        return "public-and-h5"
    return "unclassified"


def security_roles(security: Iterable[str], path: str) -> list[str]:
    names = set(security)
    roles: list[str] = []
    if "adminSession" in names:
        roles.append("employee session")
    if "csrfHeader" in names:
        roles.append("CSRF-protected mutation")
    if "openPlatformBearer" in names:
        roles.append("open-platform client")
    if not roles:
        if path.startswith("/api/public") or path.startswith("/api/h5") or path.startswith(("/q/", "/r/", "/p/", "/pay/", "/s/", "/c/")):
            roles.append("public/customer or provider callback")
        else:
            roles.append("no OpenAPI security declaration")
    return roles


def line_matches_pattern(line_text: str, prefix: str) -> bool:
    if prefix in line_text:
        return True
    # A broad mount such as /api/admin/operation-batches/ owns a documented
    # child such as /api/admin/operation-batches/covers/{digest}; compare every
    # quoted route literal in the source in both directions.
    target = prefix.rstrip("/")
    for literal in re.findall(r"[\"'](/[^\"']+)[\"']", line_text):
        literal = literal.split("?", 1)[0].rstrip("/")
        if literal == target or target.startswith(literal + "/") or literal.startswith(target + "/"):
            return True
    # Dynamic source concatenation may leave only the path family in source.
    return target in line_text and any(token in line_text for token in ("HasPrefix", "URL.Path", "TrimPrefix", "case ", "Handle("))


def collect_mounts(lines: list[SourceLine]) -> list[SourceLine]:
    mounts: list[SourceLine] = []
    for source in lines:
        if not source.path.endswith(".go") or source.path.endswith("_test.go"):
            continue
        if re.search(r"\b(?:mux|adminAPIs)\.Handle(?:Func)?\(", source.text):
            if "/" in source.text:
                mounts.append(source)
        if re.search(r"\b(?:HasPrefix|URL\.Path\s*==|path\s*==)\b", source.text) and "/" in source.text:
            # Wrapper mounts are actual routing evidence when they live in a
            # mount function, segment/radar/distribution route adapter, or the
            # canonical handler's ServeHTTP dispatch.
            if any(token in source.path for token in ("composition.go", "segment_routes.go", "radar_adapters.go", "distribution_routes.go", "openplatform/http/handler.go")):
                mounts.append(source)
    return mounts


def collect_route_evidence(lines: list[SourceLine], prefix: str) -> list[SourceLine]:
    raise RuntimeError("collect_route_evidence requires the prebuilt route index")


def collect_route_index(lines: list[SourceLine]) -> list[tuple[str, SourceLine]]:
    """Extract production route literals once instead of rescanning all files per operation."""
    entries: list[tuple[str, SourceLine]] = []
    for source in lines:
        if (
            source.path.startswith(("api/", "docs/", "scripts/", "web/scripts/"))
            or source.path.endswith(TEST_SUFFIXES)
            or source.path.endswith((".md", ".yaml"))
        ):
            continue
        for literal in re.findall(r"[\"'](/[^\"']+)[\"']", source.text):
            if literal.startswith(("/api/", "/admin/", "/auth/", "/wecom/", "/oauth/", "/open/", "/mcp", "/q/", "/r/", "/p/", "/pay/", "/s/", "/c/", "/d/")):
                normalized = literal.split("?", 1)[0].rstrip("/")
                # Generic dispatcher trims such as /api/admin/ or /api/v1/
                # do not prove any individual documented operation.
                if normalized in {"/api", "/api/admin", "/api/v1", "/admin", "/mcp"}:
                    continue
                entries.append((normalized, source))
    return entries


def route_evidence_from_index(index: list[tuple[str, SourceLine]], prefix: str) -> list[SourceLine]:
    target = prefix.rstrip("/")
    evidence: list[SourceLine] = []
    seen: set[tuple[str, int]] = set()
    for literal, source in index:
        if literal == target or target.startswith(literal + "/") or literal.startswith(target + "/"):
            key = (source.path, source.line)
            if key not in seen:
                seen.add(key)
                evidence.append(source)
    return evidence


def route_clue_kind(source: SourceLine) -> str:
    if source.path.startswith("web/") or source.path.endswith((".js", ".mjs", ".ts")):
        return "frontend_or_client_string_clue"
    if source.path.endswith(".go"):
        return "production_source_string_clue"
    return "source_string_clue"


def collect_tests(test_data: list[tuple[str, str]], operation: dict[str, Any]) -> list[dict[str, str]]:
    prefix = path_prefix(operation["path"])
    op_id = operation.get("operation_id", "")
    hits: list[dict[str, str]] = []
    for filename, content in test_data:
        matched_by = []
        if prefix and prefix in content:
            matched_by.append("path_prefix")
        if op_id and op_id in content:
            matched_by.append("operation_id")
        # A module-level test is useful context for UI/page operations whose
        # route is intentionally asserted through adapters rather than literal
        # OpenAPI paths, but it is marked separately from a direct route hit.
        module = module_for(operation)
        module_tokens = {
            "access": ("access", "login"),
            "oneid": ("oneid", "identity"),
            "transactions": ("order", "payment", "refund", "commerce"),
            "distribution": ("distribution",),
            "group-operations": ("group_ops", "groupops", "group-ops"),
        }.get(module, (module.replace("-", "_"),))
        if not matched_by and any(token in filename.lower() or token in content[:1000].lower() for token in module_tokens):
            matched_by.append("module_test")
        if matched_by:
            hits.append(
                {
                    "file": filename,
                    "matched_by": "+".join(matched_by),
                    "http_method_assertion_verified": False,
                }
            )
    hits.sort(key=lambda item: ("path_prefix" not in item["matched_by"], item["file"]))
    return hits[:20]


CAPABILITIES = [
    {
        "id": "access-and-role-governance",
        "module": "access",
        "goal": "登录、退出、员工目录、角色/登录权限、停用、超管转移与安全配置边界",
        "role_dimensions": ["unauthenticated", "viewer", "admin", "super-admin"],
        "state_dimensions": ["active", "disabled", "role changed", "CSRF rejected", "session expired"],
        "operation_prefixes": ["/login", "/logout", "/api/admin/access", "/api/admin/admin-access", "/admin/config/login-access"],
        "test_globs": ["access", "admin_shell", "role", "permission", "login"],
        "gate": "security-critical",
    },
    {
        "id": "oneid-and-customer-identity",
        "module": "oneid",
        "goal": "身份解析、pending/conflict、客户根、合并候选与反向合并；禁止猜测归属",
        "role_dimensions": ["admin", "viewer", "provider-verified adapter"],
        "state_dimensions": ["pending", "verified", "conflict", "merge candidate", "merged", "reversed"],
        "operation_prefixes": ["/api/admin/oneid", "/api/admin/customers", "/open/v1/customers"],
        "test_globs": ["oneid", "identity", "customer", "cutover"],
        "gate": "identity-critical",
    },
    {
        "id": "customer-directory-and-sync",
        "module": "customer",
        "goal": "客户列表/详情/360、客户同步、owner/tag/timeline/chat 只读投影与回读",
        "role_dimensions": ["viewer", "admin", "super-admin"],
        "state_dimensions": ["queued", "running", "completed", "failed", "partial/unresolved"],
        "operation_prefixes": ["/api/admin/customers", "/api/admin/customer-sync-runs", "/api/admin/hxc-dashboard"],
        "test_globs": ["customer", "sync", "hxc", "sidebar", "owner_handoff"],
        "gate": "core-business",
    },
    {
        "id": "wecom-channel-acquisition",
        "module": "channel-acquisition",
        "goal": "渠道定义、客服分配、二维码/资产、回调接纳、欢迎语/tag 受理与回执",
        "role_dimensions": ["viewer", "admin", "super-admin", "provider callback"],
        "state_dimensions": ["draft", "active", "archived", "accepted", "executed", "receipt unknown", "reconciled"],
        "operation_prefixes": ["/api/admin/channels", "/api/admin/wecom-customer-acquisition-links", "/wecom/external-contact/callback", "/api/admin/wecom/callback-receipts"],
        "test_globs": ["channel", "welcome", "callback", "entrant", "wecom"],
        "gate": "provider-effect",
    },
    {
        "id": "transactions-payment-refund",
        "module": "transactions",
        "goal": "商品购买、支付回调、订单/退款、对账、断线恢复与未知结果原键处理",
        "role_dimensions": ["public buyer", "admin", "finance operator (actual Access role mapping required)", "provider callback"],
        "state_dimensions": ["created", "pending", "paid", "failed", "outcome_unknown", "refunded", "reconciled"],
        "operation_prefixes": ["/api/v1/wechat-pay", "/api/admin/orders", "/api/admin/refunds", "/api/admin/payments", "/api/public/wechat-pay"],
        "test_globs": ["commerce", "payment", "refund", "order", "checkout", "transaction"],
        "gate": "funds-critical",
    },
    {
        "id": "first-level-distribution",
        "module": "distribution",
        "goal": "分销注册/资格、推广归因、佣金、退款扣回、到期结算、对账与管理员例外",
        "role_dimensions": ["customer/distributor", "admin", "finance operator (actual Access role mapping required)"],
        "state_dimensions": ["eligible", "attributed", "pending", "due", "paid", "refunded", "exception", "reconciled"],
        "operation_prefixes": ["/api/v1/distribution", "/api/admin/distribution", "/d/"],
        "test_globs": ["distribution", "commission", "qualification", "settlement"],
        "gate": "funds-critical",
    },
    {
        "id": "products-entitlements-coupons",
        "module": "products/coupons",
        "goal": "商品与周期权益、优惠券领取/预占/核销/释放、会员数据与分享",
        "role_dimensions": ["public buyer", "admin", "viewer"],
        "state_dimensions": ["draft", "enabled", "disabled", "claimed", "reserved", "redeemed", "released", "expired"],
        "operation_prefixes": ["/api/v1/products", "/api/admin/service-period-products", "/api/admin/coupons", "/api/h5/coupons"],
        "test_globs": ["product", "coupon", "entitlement", "member_grid"],
        "gate": "core-business",
    },
    {
        "id": "group-operations-and-outbound",
        "module": "group-operations",
        "goal": "群目录、计划/节点、素材冻结、广播/webhook、暂停恢复、受理/执行/送达/对账",
        "role_dimensions": ["viewer", "operator (actual Access role mapping required)", "admin", "provider directory"],
        "state_dimensions": ["draft", "paused", "running", "accepted", "executed", "outcome_unknown", "reconciled"],
        "operation_prefixes": ["/api/admin/automation-conversion/group-ops", "/api/automation/group-ops", "/api/admin/external-effects"],
        "test_globs": ["group_ops", "groupops", "webhook", "outbound", "external_effect"],
        "gate": "provider-effect",
    },
    {
        "id": "automation-and-ai-approval",
        "module": "automation/ai-assistant",
        "goal": "自动化/AI 计划、固定内容、人群、人工审阅、审批后执行与逐项回执",
        "role_dimensions": ["viewer", "operator (actual Access role mapping required)", "admin", "super-admin"],
        "state_dimensions": ["draft", "prechecked", "pending review", "approved", "rejected", "queued", "unknown", "reconciled"],
        "operation_prefixes": ["/api/admin/automation-agents", "/api/admin/automations", "/api/admin/ai-assistant", "/api/admin/operation-batches", "/api/integrations/ai-assistant"],
        "test_globs": ["automation", "aiassistant", "ai_assistant", "operation_batch", "outbound"],
        "gate": "provider-effect",
    },
    {
        "id": "survey-and-public-h5",
        "module": "survey",
        "goal": "问卷定义、发布/停用、公开提交、OAuth、结果授权、历史与外推",
        "role_dimensions": ["admin", "viewer", "public respondent", "OAuth verified"],
        "state_dimensions": ["draft", "published", "disabled", "submitted", "unresolved", "external effect unknown"],
        "operation_prefixes": ["/api/admin/questionnaires", "/api/public/questionnaires", "/api/h5/surveys", "/q/"],
        "test_globs": ["survey", "questionnaire", "oauth", "submission"],
        "gate": "core-business",
    },
    {
        "id": "media-and-preparation",
        "module": "media",
        "goal": "图片/附件/小程序/群邀请素材生命周期、私有下载、准备快照与刷新失败分类",
        "role_dimensions": ["admin", "viewer", "provider credential"],
        "state_dimensions": ["local", "prepared", "queued", "succeeded", "failed", "outcome_unknown", "expired"],
        "operation_prefixes": ["/api/admin/image-library", "/api/admin/attachment-library", "/api/admin/miniprogram-library", "/api/admin/media-preparations"],
        "test_globs": ["media", "material", "attachment", "image", "refresh"],
        "gate": "provider-effect",
    },
    {
        "id": "open-platform-and-api-docs",
        "module": "open-platform",
        "goal": "OAuth/token、机器客户端、能力/路由、客户/订单/记录只读与 AI 计划写入",
        "role_dimensions": ["open-platform client", "super-admin", "admin"],
        "state_dimensions": ["created", "active", "disabled", "rotated", "revoked", "audit read"],
        "operation_prefixes": ["/oauth/token", "/mcp", "/open/v1", "/api/admin/open-platform"],
        "test_globs": ["open_platform", "openplatform", "api_docs", "client"],
        "gate": "security-critical",
    },
    {
        "id": "runtime-config-release-and-readiness",
        "module": "config/platform",
        "goal": "配置分类、草稿/校验/发布/回滚、迁移 readiness、健康和版本回读",
        "role_dimensions": ["viewer", "admin", "super-admin"],
        "state_dimensions": ["draft", "validated", "published", "rolled back", "blocked", "ready", "unready"],
        "operation_prefixes": ["/api/admin/config", "/api/admin/setup-wizard", "/healthz", "/readyz"],
        "test_globs": ["readiness", "runtime_config", "config", "install_release", "migration"],
        "gate": "release-critical",
    },
]


def capability_evidence(root: Path, files: list[tuple[str, str]], tests: list[tuple[str, str]], cap: dict[str, Any]) -> dict[str, Any]:
    operation_paths: list[str] = []
    test_files_found: list[str] = []
    for path, content in files:
        if path.endswith(TEST_SUFFIXES) or path.endswith((".md", ".yaml")):
            continue
        if any(prefix in content for prefix in cap["operation_prefixes"]):
            operation_paths.append(path)
    for path, content in tests:
        lower = path.lower()
        if any(token.lower() in lower or token.lower() in content[:2000].lower() for token in cap["test_globs"]):
            test_files_found.append(path)
    operation_paths = sorted(set(operation_paths))[:30]
    test_files_found = sorted(set(test_files_found))[:30]
    if not operation_paths:
        status = "no_static_source_match"
        gap = "No source route literal matched the capability prefixes in this candidate. Confirm whether the capability is intentionally absent or mounted through another adapter."
    elif not test_files_found:
        status = "source_without_test_reference"
        gap = "Source/module evidence exists but no test-file mapping was found; add an isolated role/state journey before release review."
    else:
        status = "text_reference_candidate_not_run"
        gap = "Existing test-file references are heuristic text matches only; HTTP method/assertion coverage, a candidate-bound run receipt and business/provider readback are still required."
    return {
        "id": cap["id"],
        "module": cap["module"],
        "goal": cap["goal"],
        "role_dimensions": cap["role_dimensions"],
        "state_dimensions": cap["state_dimensions"],
        "operation_prefixes": cap["operation_prefixes"],
        "source_evidence_files": operation_paths,
        "test_evidence_files": test_files_found,
        "coverage_status": status,
        "release_proof": "not_proven",
        "role_mapping_required": "Role labels are test dimensions only; map each to the actual Access role and enforce it in API and browser checks before treating a case as covered.",
        "gate": cap["gate"],
        "gap": gap,
    }


def source_git_metadata(root: Path, expected_sha: str) -> dict[str, Any]:
    """Validate that inventory evidence comes from the requested clean source."""

    def git(*args: str) -> str:
        completed = subprocess.run(
            ["git", *args],
            cwd=root,
            check=True,
            text=True,
            capture_output=True,
        )
        return completed.stdout.strip()

    head = git("rev-parse", "HEAD")
    tree = git("rev-parse", "HEAD^{tree}")
    unstaged = subprocess.run(["git", "diff", "--quiet", "--"], cwd=root).returncode == 0
    staged = subprocess.run(["git", "diff", "--cached", "--quiet", "--"], cwd=root).returncode == 0
    if head != expected_sha:
        raise SystemExit(f"source HEAD {head} does not match requested candidate {expected_sha}")
    if not (unstaged and staged):
        raise SystemExit("source has tracked changes; inventory requires a clean tracked tree")
    return {
        "root": str(root),
        "head": head,
        "tree": tree,
        "tracked_diff_clean": True,
    }


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", "--worktree", dest="root", type=Path, default=Path.cwd())
    parser.add_argument("--sha", required=True)
    parser.add_argument("--output-dir", type=Path, required=True)
    args = parser.parse_args()
    root = args.root.resolve()
    out = args.output_dir.resolve()
    out.mkdir(parents=True, exist_ok=True)
    source_git = source_git_metadata(root, args.sha)

    operations = parse_openapi(root)
    lines = iter_source_lines(root)
    tests = test_files(root)
    mounts = collect_mounts(lines)
    route_index = collect_route_index(lines)

    rows: list[dict[str, Any]] = []
    for operation in operations:
        prefix = path_prefix(operation["path"])
        mount_hits = [s for s in mounts if line_matches_pattern(s.text, prefix)]
        route_hits = route_evidence_from_index(route_index, prefix)
        test_hits = collect_tests(tests, operation)
        direct_test_hits = [hit for hit in test_hits if "path_prefix" in hit["matched_by"] or "operation_id" in hit["matched_by"]]
        if not mount_hits and route_hits:
            coverage_status = "route_clue_without_mount"
            gap = "A source string clue exists, but no static Go mount evidence was found for this method/path. The clue is not proof of runtime reachability; confirm the actual Composition Root/wrapper mount with an HTTP check."
        elif not mount_hits and not route_hits:
            coverage_status = "mount_not_found_by_static_scan"
            gap = "No static Go mount or source string clue was found for this documented operation. This heuristic does not prove a runtime defect; verify generated/implicit routing or stale documentation with a runtime check."
        elif not test_hits:
            coverage_status = "mounted_no_text_reference"
            gap = "Static mount evidence exists but no matching test-file text reference was found; add method-specific authenticated/negative and business readback coverage."
        elif not direct_test_hits:
            coverage_status = "module_text_reference_only"
            gap = "Only a broad module test text reference was found; no test file references this method/path family directly. HTTP method/assertion coverage remains unverified."
        else:
            coverage_status = "text_reference_candidate"
            gap = "A test file text reference was found by path prefix or operation id, but HTTP method/assertion coverage, run receipt and provider/business outcome were not evaluated by this inventory."
        rows.append(
            {
                **operation,
                "module": module_for(operation),
                "security_roles": security_roles(operation["security"], operation["path"]),
                "documented": True,
                "mount_evidence": [
                    {"file": hit.path, "line": hit.line, "text": hit.text.strip()[:240]}
                    for hit in mount_hits[:8]
                ],
                "route_source_clues": [
                    {
                        "file": hit.path,
                        "line": hit.line,
                        "text": hit.text.strip()[:240],
                        "kind": route_clue_kind(hit),
                    }
                    for hit in route_hits[:8]
                ],
                "test_evidence": test_hits,
                "test_reference_basis": "path_prefix_or_operation_id_text; module filename/content token for module-only rows",
                "test_method_assertion_verified": False,
                "test_run_receipt": None,
                "mount_evidence_scope": "static Go Handle/dispatch source only; no runtime HTTP proof",
                "route_source_clue_scope": "source/frontend string clue only; never mount proof",
                "coverage_status": coverage_status,
                "release_proof": "not_proven",
                "gap": gap,
            }
        )

    capability_rows = [capability_evidence(root, lines_to_files(lines), tests, cap) for cap in CAPABILITIES]
    status_counts = Counter(row["coverage_status"] for row in rows)
    module_counts: dict[str, dict[str, int]] = defaultdict(lambda: defaultdict(int))
    for row in rows:
        module_counts[row["module"]][row["coverage_status"]] += 1

    metadata = {
        "candidate_sha": args.sha,
        "worktree": str(root),
        "output_dir": str(out),
        "source_git": source_git,
        "generated_at": datetime.now(timezone.utc).isoformat(),
        "sources": {
            "openapi": "api/openapi.yaml",
            "composition_root": "cmd/aicrm/composition.go",
            "route_adapters": ["cmd/aicrm/segment_routes.go", "cmd/aicrm/radar_adapters.go", "cmd/aicrm/distribution_routes.go", "internal/openplatform/http/handler.go"],
            "tests": "all files matching *_test.go, *.test.{mjs,ts,js,py,sh}",
        },
        "counts": {
            "openapi_paths": len({row["path"] for row in rows}),
            "openapi_operations": len(rows),
            "source_lines_scanned": len(lines),
            "test_files_scanned": len(tests),
            "coverage_status": dict(sorted(status_counts.items())),
            "module_status": {module: dict(sorted(values.items())) for module, values in sorted(module_counts.items())},
        },
        "interpretation": {
            "text_reference_is_not_test_coverage": True,
            "test_method_assertion_verified": False,
            "test_run_receipt": None,
            "mount_evidence_is_static_only": True,
            "route_source_clues_are_not_mount_proof": True,
            "release_proof_default": "not_proven",
            "external_effects": "Provider accepted/queued/executed/receipt/reconciled states require isolated run evidence and actual business/provider readback.",
            "identity": "Identity/customer flows require canonical OneID evidence and pending/conflict behavior; route presence does not prove attribution safety.",
            "role_dimensions": "finance operator/operator labels are test dimensions only and require mapping to actual Access roles.",
        },
    }
    payload = {"metadata": metadata, "operations": rows, "capabilities": capability_rows}
    (out / "coverage-matrix.json").write_text(json.dumps(payload, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")

    csv_fields = [
        "module", "method", "path", "operation_id", "tags", "security", "security_roles", "response_codes",
        "documented", "mount_evidence", "route_source_clues", "test_evidence", "test_reference_basis",
        "test_method_assertion_verified", "coverage_status", "release_proof", "gap",
    ]
    with (out / "coverage-matrix.csv").open("w", encoding="utf-8", newline="") as handle:
        writer = csv.DictWriter(handle, fieldnames=csv_fields, lineterminator="\n")
        writer.writeheader()
        for row in rows:
            writer.writerow({
                "module": row["module"],
                "method": row["method"],
                "path": row["path"],
                "operation_id": row["operation_id"],
                "tags": ";".join(row["tags"]),
                "security": ";".join(row["security"]),
                "security_roles": ";".join(row["security_roles"]),
                "response_codes": ";".join(row["response_codes"]),
                "documented": str(row["documented"]).lower(),
                "mount_evidence": ";".join(f"{item['file']}:{item['line']}" for item in row["mount_evidence"]),
                "route_source_clues": ";".join(f"{item['file']}:{item['line']}[{item['kind']}]" for item in row["route_source_clues"]),
                "test_evidence": ";".join(f"{item['file']}[{item['matched_by']}]" for item in row["test_evidence"]),
                "test_reference_basis": row["test_reference_basis"],
                "test_method_assertion_verified": str(row["test_method_assertion_verified"]).lower(),
                "coverage_status": row["coverage_status"],
                "release_proof": row["release_proof"],
                "gap": row["gap"],
            })

    urgent = [
        {
            "id": "U1",
            "priority": "P0",
            "area": "transactions/payment/refund",
            "why_now": "Money movement and outcome_unknown paths must be proven before any release decision.",
            "source_evidence": ["api/openapi.yaml (Transactions operations)", "cmd/aicrm/composition.go:2310-2319 (payment/refund mounts)"],
            "test_evidence": [
                "cmd/aicrm/commerce_checkout_integration_test.go",
                "cmd/aicrm/commerce_funds_http_integration_test.go",
                "internal/payment/app/service_refund_postgres_integration_test.go",
                "internal/payment/http/handler_test.go",
                "internal/payment/store/postgres_integration_test.go",
            ],
            "gap": "Inventory maps routes/tests but has no candidate-bound run receipt proving callback replay, timeout/outcome_unknown, refund idempotency, reconciliation and post-state readback.",
            "next_test": "Isolated PostgreSQL/provider protocol: create checkout -> signed payment callback replay/乱序 -> outcome_unknown -> original-key lookup/reconcile -> refund -> order, entitlement and ledger readback; assert no duplicate external effect.",
        },
        {
            "id": "U2",
            "priority": "P0",
            "area": "distribution",
            "why_now": "The frozen SHA introduces the first-level referral lifecycle; qualification, commission and refund settlement are release-critical.",
            "source_evidence": ["internal/distribution/http/handler.go:114-130", "cmd/aicrm/distribution_routes.go:34-36", "docs/contracts/2026-09-14-first-level-distribution.md"],
            "test_evidence": ["internal/distribution/http/handler_test.go", "internal/distribution/http/admin_test.go", "internal/distribution/domain/distribution_test.go", "cmd/aicrm/distribution_settlement_refund_postgres_integration_test.go"],
            "gap": "Existing tests are mapped, but this inventory cannot prove the complete user journey on dcfc022: registration/attribution -> paid order -> refund adjustment -> due/settled commission -> reconciliation and payer/beneficiary separation.",
            "next_test": "Run a candidate-bound isolated journey with two customers, duplicate referral links, paid order, partial/full refund, due worker restart and reconciliation; verify commission exactly once and correct customer roots.",
        },
        {
            "id": "U3",
            "priority": "P0",
            "area": "OneID/identity and access",
            "why_now": "Identity misattribution or role bypass blocks release even when route/unit tests pass.",
            "source_evidence": ["internal/identity/http/handler.go:175-370", "cmd/aicrm/composition.go:2276-2281", "internal/access/http"],
            "test_evidence": ["internal/identity/http/handler_test.go", "cmd/aicrm/access_governance_ui_chromium_postgres_integration_test.go", "cmd/aicrm/customer_sync_integration_test.go"],
            "gap": "No evidence in this inventory establishes the candidate run across pending/conflict/verified identity, cross-customer rejection, role migration, server enforcement, browser visibility and super-admin-only security configuration.",
            "next_test": "Use isolated fixtures for viewer/admin/super-admin plus two customer roots: assert denied cross-root access, unresolved identities remain pending/conflict, role demotion takes effect in API and browser, and only super-admin can change security config.",
        },
        {
            "id": "U4",
            "priority": "P0",
            "area": "external effects/outcome_unknown",
            "why_now": "Group, channel welcome, automation and AI writes share the outbound/effects reliability boundary.",
            "source_evidence": ["internal/externaleffects/port", "cmd/aicrm/composition.go:2323-2325", "cmd/aicrm/composition.go:1945-1947"],
            "test_evidence": ["cmd/aicrm/group_ai_joint_runtime_integration_test.go", "cmd/aicrm/group_ops_runtime_integration_test.go", "web/scripts/outbound-task-history-e2e.mjs"],
            "gap": "Existing mappings do not constitute provider receipt proof. Need one candidate run covering accepted/queued/attempted/executed/outcome_unknown/reconciled, retry with original idempotency key, worker restart and no duplicate provider effect.",
            "next_test": "Run local provider protocol with deliberate timeout after provider acceptance, restart worker, reconcile by original effect id, and read back business receipt plus provider result for channel/group/AI representative operations.",
        },
        {
            "id": "U5",
            "priority": "P1",
            "area": "release migration/readiness",
            "why_now": "A clean source tree and green unit tests do not prove the release package can migrate, start, recover and expose the candidate SHA.",
            "source_evidence": ["cmd/aicrm/readiness_integration_test.go", "deploy/install-release.sh", "migrations/"],
            "test_evidence": ["cmd/aicrm/readiness_integration_test.go", "scripts/test-install-release-ordering.sh", "scripts/check-install-release-contract.sh"],
            "gap": "No candidate-bound package/migration/backup-restore receipt is included; readiness and deploy scripts remain separate from authenticated browser/provider acceptance.",
            "next_test": "Build exact dcfc022 package in isolated PostgreSQL16, run new/upgrade/interrupted migration and restore rehearsal, assert readyz/version/critical routes, then retain rollback evidence.",
        },
    ]
    gaps = {
        "candidate": metadata,
        "coverage_counts": metadata["counts"],
        "urgent_gaps": urgent,
        "capabilities": capability_rows,
        "interpretation": metadata["interpretation"],
    }
    (out / "coverage-gaps.md").write_text(render_gaps(gaps), encoding="utf-8")
    print(json.dumps({"output_dir": str(out), "counts": metadata["counts"]}, ensure_ascii=False, sort_keys=True))
    return 0


def lines_to_files(lines: list[SourceLine]) -> list[tuple[str, str]]:
    grouped: dict[str, list[str]] = defaultdict(list)
    for line in lines:
        grouped[line.path].append(line.text)
    return [(path, "\n".join(content)) for path, content in grouped.items()]


def render_gaps(gaps: dict[str, Any]) -> str:
    metadata = gaps["candidate"]
    counts = gaps["coverage_counts"]
    lines = [
        "# Release acceptance coverage gaps",
        "",
        f"Candidate: `{metadata['candidate_sha']}`",
        f"Source root: `{metadata['worktree']}`",
        f"Source HEAD/tree: `{metadata['source_git']['head']}` / `{metadata['source_git']['tree']}`",
        f"Tracked diff clean: `{metadata['source_git']['tracked_diff_clean']}`",
        f"Output directory: `{metadata['output_dir']}`",
        f"Generated: `{metadata['generated_at']}`",
        "",
        "本报告是候选版本的静态覆盖盘点，不是测试通过报告。OpenAPI method+path、源码静态挂载、路由字符串线索和测试文件文本引用均只表示存在证据入口；路由字符串线索不是实际挂载证明，测试引用未核对 HTTP method 或断言。所有 `release_proof` 保持 `not_proven`，直到在隔离环境中执行候选 SHA 并回读业务/Provider 结果。",
        "",
        f"扫描到 `{counts['openapi_operations']}` 个 OpenAPI 操作、`{counts['openapi_paths']}` 个路径、`{counts['test_files_scanned']}` 个测试文件。端点状态统计：`{json.dumps(counts['coverage_status'], ensure_ascii=False, sort_keys=True)}`。",
        "",
        "## 立即补测缺口",
        "",
    ]
    for item in gaps["urgent_gaps"]:
        lines.extend([
            f"### {item['id']} · {item['priority']} · {item['area']}",
            "",
            item["why_now"],
            "",
            f"源代码/合同证据：`{'`; `'.join(item['source_evidence'])}`。",
            "",
            f"已有测试映射：`{'`; `'.join(item['test_evidence'])}`。这些文件尚未被本盘点当作当前候选的通过收据。",
            "",
            f"缺口：{item['gap']}",
            "",
            f"建议用例：{item['next_test']}",
            "",
        ])
    lines.extend(["## 关键能力覆盖", "", "| 能力 | 角色维度 | 状态维度 | 测试映射 | 结论 |", "|---|---|---|---|---|"])
    for item in gaps["capabilities"]:
        evidence = "；".join(item["test_evidence_files"][:4]) or "无测试文件映射"
        lines.append(
            f"| {item['id']} | {', '.join(item['role_dimensions'])} | {', '.join(item['state_dimensions'])} | {evidence} | `{item['coverage_status']}` / `{item['release_proof']}` |"
        )
    lines.extend([
        "",
        "## 端点状态解释",
        "",
        "- `text_reference_candidate`：至少一个测试文件按路径前缀或 operation id 文本命中；未核对 HTTP method/断言，未执行、未绑定候选 SHA、未证明业务或 Provider 结果。",
        "- `module_text_reference_only`：只有宽泛模块测试文本引用，没有直接引用该 method/path 家族；需要补具体端点或 Journey。",
        "- `mounted_no_text_reference`：源码有静态 Go 挂载证据，但未找到测试文件文本引用。",
        "- `route_clue_without_mount`：存在源码/前端路由字符串线索，但没有静态 Go 挂载证据；线索不等于运行时不可达，需实际 HTTP 检查。",
        "- `mount_not_found_by_static_scan`：静态扫描没有找到 Go 挂载或路由字符串线索；这不单独证明运行时缺陷，需核对隐式 wrapper、生成路由或 stale OpenAPI。",
        "- 能力表中的 `finance operator`、`operator` 等角色是测试维度标签，必须映射到真实 Access role 后再判断权限覆盖。",
        "",
        "以下边界保持不变：Provider 配置、入队、HTTP 200、accepted/queued/executed、静态截图和 Mock 都不能替代实际接收/到账/交付或 reconciliation 证据；身份不确定时必须保持 pending/conflict。",
        "",
    ])
    return "\n".join(lines)


if __name__ == "__main__":
    raise SystemExit(main())
