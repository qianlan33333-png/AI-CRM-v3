#!/usr/bin/env python3
"""Build isolated-DB fixtures from the short-lived trusted source stream.

The files are consumed only by the CI rehearsal database.  They make the
rehearsal exercise the real WeCom-user and Tag-owner mapping paths without
teaching the production migrator to manufacture employees or provider tags.
"""

import csv
import json
import pathlib
import sys


def rows_by_table(path: pathlib.Path) -> dict[str, list[dict]]:
    result: dict[str, list[dict]] = {}
    current = ""
    with path.open(encoding="utf-8", errors="strict") as source:
        for raw in source:
            line = raw.strip()
            table_marker = "__AICRM_TABLE__|"
            row_marker = "__AICRM_ROW__|"
            if table_marker in line:
                marker = line[line.index(table_marker) :]
                current = marker.split("|", 2)[1]
                result.setdefault(current, [])
                continue
            if row_marker not in line or not current:
                continue
            encoded = line[line.index(row_marker) + len(row_marker) :].strip()
            result[current].append(json.loads(bytes.fromhex(encoded).decode("utf-8")))
    return result


def write_tsv(path: pathlib.Path, rows: list[list[str]]) -> None:
    with path.open("w", encoding="utf-8", newline="") as target:
        writer = csv.writer(target, delimiter="\t", quoting=csv.QUOTE_MINIMAL)
        writer.writerows(rows)
    path.chmod(0o600)


def main() -> None:
    if len(sys.argv) != 4:
        raise SystemExit("usage: prepare-channel-semantic-rehearsal-fixtures.py STREAM STAFF_TSV TAG_TSV")
    tables = rows_by_table(pathlib.Path(sys.argv[1]))
    active_assignments: dict[str, list[dict]] = {}
    for item in tables.get("automation_channel_assignee", []):
        if str(item.get("status") or "") == "inactive":
            continue
        active_assignments.setdefault(str(item.get("channel_id") or ""), []).append(item)
    assignment_profiles: dict[str, int] = {}
    for channel in tables.get("automation_channel", []):
        channel_id = str(channel.get("id") or "")
        members = active_assignments.get(channel_id, [])
        if not members and str(channel.get("owner_staff_id") or "").strip():
            members = [{"ratio_percent": 100, "max_scans_24h": 0}]
        strategy = "cap_switch" if str(channel.get("assignment_strategy") or "") == "cap_switch" else "ratio"
        ratios = sorted(int(item.get("ratio_percent") or 0) for item in members)
        caps = sorted(int(item.get("max_scans_24h") or 0) for item in members)
        if not members or (strategy == "ratio" and sum(ratios) != 100) or (strategy == "cap_switch" and any(cap < 1 for cap in caps)):
            profile = json.dumps({"strategy": strategy, "member_count": len(members), "ratios": ratios, "caps": caps}, sort_keys=True, separators=(",", ":"))
            assignment_profiles[profile] = assignment_profiles.get(profile, 0) + 1
    print("assignment_profiles=" + json.dumps(assignment_profiles, sort_keys=True, separators=(",", ":")))
    staff: dict[str, str] = {}
    for item in tables.get("automation_channel_assignee", []):
        provider_id = str(item.get("staff_id") or "").strip()
        if provider_id and str(item.get("status") or "") != "inactive":
            staff[provider_id] = str(item.get("display_name_snapshot") or provider_id).strip() or provider_id
    for item in tables.get("automation_channel", []):
        provider_id = str(item.get("owner_staff_id") or "").strip()
        if provider_id:
            staff.setdefault(provider_id, provider_id)
    write_tsv(pathlib.Path(sys.argv[2]), [[provider_id, staff[provider_id]] for provider_id in sorted(staff)])

    groups = {
        str(item.get("group_id") or "").strip(): str(item.get("group_name") or "").strip()
        for item in tables.get("wecom_corp_tag_groups", [])
        if str(item.get("group_id") or "").strip()
    }
    tags: list[list[str]] = []
    for item in tables.get("wecom_corp_tags", []):
        if item.get("deleted_at"):
            continue
        tag_id = str(item.get("tag_id") or "").strip()
        group_id = str(item.get("group_id") or "").strip()
        tag_name = str(item.get("tag_name") or "").strip()
        group_name = str(item.get("group_name") or groups.get(group_id) or "").strip()
        if tag_id and group_id and tag_name and group_name:
            tags.append([tag_id, group_id, tag_name, group_name])
    write_tsv(pathlib.Path(sys.argv[3]), sorted(tags))


if __name__ == "__main__":
    main()
