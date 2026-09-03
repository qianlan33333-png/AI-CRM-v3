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
previous=""
if [[ -L "$current_link" ]]; then
	previous="$(readlink -f "$current_link")"
fi

install -d -m 0755 "$release_root"
if [[ -e "$release_dir" ]]; then
  if [[ ! -d "$release_dir" || -L "$release_dir" ]]; then
    echo "existing release path is not a directory" >&2
    exit 4
  fi
  echo "resuming validated release ${release_sha}"
else
  staging_dir="$(mktemp -d "${release_root}/.${release_sha}.staging.XXXXXX")"
  cleanup_staging() {
    if [[ -n "${staging_dir:-}" && -d "$staging_dir" ]]; then
      rm -rf -- "$staging_dir"
    fi
  }
  trap cleanup_staging EXIT
  tar -xzf "$archive" -C "$staging_dir"
  test -f "$staging_dir/release-files.sha256"
  (cd "$staging_dir" && sha256sum --strict --check release-files.sha256)
  mv -T "$staging_dir" "$release_dir"
  staging_dir=""
  trap - EXIT
fi
test -x "$release_dir/bin/aicrm"
test -x "$release_dir/bin/migrate-platform"
test -x "$release_dir/bin/migrate-river"
test -x "$release_dir/bin/migrate-phone-identities"
test -f "$release_dir/migrations/0005_external_effects.sql"
test -f "$release_dir/migrations/0006_wecom_callback_channel_acquisition.sql"
test -f "$release_dir/migrations/0007_media.sql"
test -f "$release_dir/migrations/0008_tag_catalog.sql"
test -f "$release_dir/migrations/0009_customer_activation.sql"
test -f "$release_dir/migrations/0010_product.sql"
test -f "$release_dir/migrations/0011_coupon_rules.sql"
test -f "$release_dir/migrations/0012_group_ops.sql"
test -f "$release_dir/migrations/0013_automation_agents.sql"
test -f "$release_dir/migrations/0016_media_content_packages.sql"
test -f "$release_dir/migrations/0017_group_ops_history.sql"
test -f "$release_dir/migrations/0019_tag_catalog_sync_projection.sql"
test -f "$release_dir/web/dist/asset-manifest.json"
test -f "$release_dir/release-files.sha256"
(cd "$release_dir" && sha256sum --strict --check release-files.sha256)
printf 'AICRM_RELEASE_SHA=%s\n' "$release_sha" > "$release_dir/release.env"
chown -R aicrm:aicrm "$release_dir"

ln -sfn "$release_dir" "${current_link}.new"
mv -Tf "${current_link}.new" "$current_link"
install -m 0644 "$release_dir/deploy/aicrm.service" /etc/systemd/system/aicrm.service
install -m 0644 "$release_dir/deploy/aicrm-migrate.service" /etc/systemd/system/aicrm-migrate.service
install -m 0644 "$release_dir/deploy/aicrm-wecom-worker.service" /etc/systemd/system/aicrm-wecom-worker.service
install -m 0644 "$release_dir/deploy/aicrm-wecom-worker.timer" /etc/systemd/system/aicrm-wecom-worker.timer
install -m 0644 "$release_dir/deploy/aicrm-effects-worker.service" /etc/systemd/system/aicrm-effects-worker.service
install -m 0644 "$release_dir/deploy/aicrm-customer-sync-daily.service" /etc/systemd/system/aicrm-customer-sync-daily.service
install -m 0644 "$release_dir/deploy/aicrm-customer-sync-daily.timer" /etc/systemd/system/aicrm-customer-sync-daily.timer
systemctl daemon-reload

rollback() {
  if [[ -n "$previous" && -d "$previous" ]]; then
    ln -sfn "$previous" "${current_link}.rollback"
    mv -Tf "${current_link}.rollback" "$current_link"
    systemctl restart aicrm.service || true
    systemctl restart aicrm-wecom-worker.timer || true
    systemctl restart aicrm-effects-worker.service || true
    systemctl restart aicrm-customer-sync-daily.timer || true
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
if ! systemctl enable aicrm-effects-worker.service || ! systemctl restart aicrm-effects-worker.service; then
  rollback
  exit 8
fi
effects_worker_pid="$(systemctl show aicrm-effects-worker.service -p MainPID --value)"
if ! systemctl is-active --quiet aicrm-effects-worker.service || \
  [[ "$(readlink -f "/proc/${effects_worker_pid}/exe")" != "$release_dir/bin/aicrm" ]]; then
  rollback
  exit 9
fi
if ! systemctl enable --now aicrm-customer-sync-daily.timer; then
  rollback
  exit 10
fi
echo "release ${release_sha} active"
