#!/usr/bin/env bash
set -euo pipefail

archive="${1:-}"
release_sha="${2:-}"
release_root=/opt/aicrm/releases
current_link=/opt/aicrm/current

if [[ ! "$release_sha" =~ ^[0-9a-f]{40}$ ]]; then
  echo "invalid release sha" >&2
  exit 2
fi
if [[ "$archive" != "/tmp/aicrm-${release_sha}.tar.gz" || ! -f "$archive" ]]; then
  echo "invalid release archive" >&2
  exit 2
fi
if ! id aicrm >/dev/null 2>&1 || [[ ! -f /etc/aicrm/aicrm.env ]]; then
  echo "aicrm runtime is not provisioned" >&2
  exit 3
fi

release_dir="${release_root}/${release_sha}"
if [[ -e "$release_dir" ]]; then
  echo "release already exists" >&2
  exit 4
fi
previous=""
if [[ -L "$current_link" ]]; then
	previous="$(readlink -f "$current_link")"
fi

install -d -m 0755 "$release_root"
install -d -m 0755 "$release_dir"
tar -xzf "$archive" -C "$release_dir"
test -x "$release_dir/bin/aicrm"
test -x "$release_dir/bin/migrate-platform"
test -x "$release_dir/bin/migrate-river"
test -f "$release_dir/migrations/0005_external_effects.sql"
test -f "$release_dir/web/dist/asset-manifest.json"
printf 'AICRM_RELEASE_SHA=%s\n' "$release_sha" > "$release_dir/release.env"
chown -R aicrm:aicrm "$release_dir"

ln -sfn "$release_dir" "${current_link}.new"
mv -Tf "${current_link}.new" "$current_link"
install -m 0644 "$release_dir/deploy/aicrm.service" /etc/systemd/system/aicrm.service
install -m 0644 "$release_dir/deploy/aicrm-migrate.service" /etc/systemd/system/aicrm-migrate.service
install -m 0644 "$release_dir/deploy/aicrm-wecom-worker.service" /etc/systemd/system/aicrm-wecom-worker.service
install -m 0644 "$release_dir/deploy/aicrm-wecom-worker.timer" /etc/systemd/system/aicrm-wecom-worker.timer
install -m 0644 "$release_dir/deploy/aicrm-effects-worker.service" /etc/systemd/system/aicrm-effects-worker.service
systemctl daemon-reload

rollback() {
  if [[ -n "$previous" && -d "$previous" ]]; then
    ln -sfn "$previous" "${current_link}.rollback"
    mv -Tf "${current_link}.rollback" "$current_link"
    systemctl restart aicrm.service || true
    systemctl restart aicrm-wecom-worker.timer || true
    systemctl restart aicrm-effects-worker.service || true
  fi
}

if ! systemctl start aicrm-migrate.service; then
  rollback
  exit 5
fi
if ! systemctl restart aicrm.service; then
  rollback
  exit 6
fi

ready=false
for _ in $(seq 1 30); do
  if curl --fail --silent --show-error http://127.0.0.1:8080/readyz >/dev/null; then
    ready=true
    break
  fi
  sleep 1
done
if [[ "$ready" != true ]]; then
  rollback
  exit 7
fi

systemctl enable --now aicrm-wecom-worker.timer
if ! systemctl enable --now aicrm-effects-worker.service; then
  rollback
  exit 8
fi
echo "release ${release_sha} active"
