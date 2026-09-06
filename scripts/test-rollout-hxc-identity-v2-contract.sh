#!/usr/bin/env bash
set -euo pipefail

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
script="$repository_root/deploy/rollout-hxc-identity-v2.sh"
domain="$repository_root/internal/hxcdashboard/domain/domain.go"
worker="$repository_root/cmd/aicrm/main.go"

rule_version="$(sed -nE 's/^const RuleVersion = "(hxc-current-v[0-9]+)"$/\1/p' "$domain")"
[[ -n "$rule_version" && "$(printf '%s\n' "$rule_version" | wc -l | tr -d ' ')" == 1 ]] || {
  echo 'HXC projection rule version must have one explicit domain value' >&2
  exit 1
}
grep -qxF "hxc_projection_rule_version=$rule_version" "$script" || {
  echo 'HXC rollout must validate the current projection rule version' >&2
  exit 1
}

rule_predicate="rule_version='\${hxc_projection_rule_version}'"
[[ "$(grep -cF "$rule_predicate" "$script")" == 3 ]] || {
  echo 'HXC rollout must use its current rule version for inspect, apply, and scheduled projections' >&2
  exit 1
}

# The durable trigger keys predate the v3 projection and deliberately retain
# their v2 namespace. The rollout gate must wait for those exact runtime keys.
grep -qF 'key := "initial:hxc-dashboard-v2:" + mode' "$worker"
grep -qF 'key = "scheduled:" + time.Now().In(location).Format("2006-01-02T15") + ":hxc-dashboard-v2:" + mode' "$worker"
grep -qF 'wait_for_run "$mode" "initial:hxc-dashboard-v2:${mode}:${release_sha}"' "$script"
grep -qF 'scheduled_key="scheduled:$(TZ=Asia/Shanghai date +%Y-%m-%dT%H):hxc-dashboard-v2:apply"' "$script"

bash -n "$script"
echo 'HXC rollout rule and durable-key contract passed'
