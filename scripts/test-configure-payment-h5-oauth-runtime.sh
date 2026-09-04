#!/usr/bin/env bash
set -euo pipefail

repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
test_root="$(mktemp -d)"
trap 'rm -rf -- "$test_root"' EXIT

runtime_env="$test_root/aicrm.env"
source_config="$test_root/payment-h5.env"
release_sha=1111111111111111111111111111111111111111

cat > "$runtime_env" <<'EOF'
AICRM_DATABASE_URL=postgres://example
AICRM_PUBLIC_ORIGIN=https://example.invalid
AICRM_WECHAT_PAY_PROVIDER_ENABLED=true
AICRM_WECHAT_PAY_H5_OAUTH_ENABLED=false
AICRM_WECHAT_PAY_H5_APP_ID=wx0000000000000000
AICRM_WECHAT_PAY_H5_APP_SECRET=00000000000000000000000000000000
AICRM_WECHAT_PAY_H5_APP_SCOPE=wechat-app:wx0000000000000000
EOF
cat > "$source_config" <<'EOF'
AICRM_WECHAT_PAY_H5_OAUTH_ENABLED=true
AICRM_WECHAT_PAY_H5_APP_ID=wx1111111111111111
AICRM_WECHAT_PAY_H5_APP_SECRET=11111111111111111111111111111111
AICRM_WECHAT_PAY_H5_APP_SCOPE=wechat-app:wx1111111111111111
EOF
chmod 0600 "$runtime_env" "$source_config"

AICRM_RUNTIME_ENV_FILE="$runtime_env" \
  "$repository_root/deploy/configure-payment-h5-oauth-runtime.sh" "$source_config" "$release_sha" >/dev/null

grep -qxF 'AICRM_DATABASE_URL=postgres://example' "$runtime_env"
grep -qxF 'AICRM_PUBLIC_ORIGIN=https://example.invalid' "$runtime_env"
grep -qxF 'AICRM_WECHAT_PAY_PROVIDER_ENABLED=true' "$runtime_env"
grep -qxF 'AICRM_WECHAT_PAY_H5_OAUTH_ENABLED=true' "$runtime_env"
grep -qxF 'AICRM_WECHAT_PAY_H5_APP_ID=wx1111111111111111' "$runtime_env"
grep -qxF 'AICRM_WECHAT_PAY_H5_APP_SCOPE=wechat-app:wx1111111111111111' "$runtime_env"
[[ "$(grep -c '^AICRM_WECHAT_PAY_H5_' "$runtime_env")" == 4 ]]
contact_key="$(sed -n 's/^AICRM_ORDER_CONTACT_DATA_KEY=//p' "$runtime_env")"
[[ "$contact_key" =~ ^[A-Za-z0-9+/]{43}$ ]]
[[ "$(stat -c '%a' "$runtime_env" 2>/dev/null || stat -f '%Lp' "$runtime_env")" == 600 ]]
[[ ! -e "$source_config" ]]

source_config="$test_root/payment-h5-repeat.env"
cat > "$source_config" <<'EOF'
AICRM_WECHAT_PAY_H5_OAUTH_ENABLED=true
AICRM_WECHAT_PAY_H5_APP_ID=wx1111111111111111
AICRM_WECHAT_PAY_H5_APP_SECRET=11111111111111111111111111111111
AICRM_WECHAT_PAY_H5_APP_SCOPE=wechat-app:wx1111111111111111
EOF
chmod 0600 "$source_config"
AICRM_RUNTIME_ENV_FILE="$runtime_env" \
  "$repository_root/deploy/configure-payment-h5-oauth-runtime.sh" "$source_config" "$release_sha" >/dev/null
[[ "$(sed -n 's/^AICRM_ORDER_CONTACT_DATA_KEY=//p' "$runtime_env")" == "$contact_key" ]]

before="$(shasum -a 256 "$runtime_env")"
bad_config="$test_root/bad.env"
cat > "$bad_config" <<'EOF'
AICRM_WECHAT_PAY_H5_OAUTH_ENABLED=true
AICRM_WECHAT_PAY_H5_APP_ID=wx1111111111111111
AICRM_WECHAT_PAY_H5_APP_SECRET=11111111111111111111111111111111
AICRM_WECHAT_PAY_H5_APP_SCOPE=wechat-app:wx2222222222222222
EOF
chmod 0600 "$bad_config"
if AICRM_RUNTIME_ENV_FILE="$runtime_env" \
  "$repository_root/deploy/configure-payment-h5-oauth-runtime.sh" "$bad_config" "$release_sha" >/dev/null 2>&1; then
  echo "mismatched H5 OAuth scope unexpectedly succeeded" >&2
  exit 1
fi
[[ "$(shasum -a 256 "$runtime_env")" == "$before" ]]

disabled_runtime="$test_root/disabled.env"
sed 's/^AICRM_WECHAT_PAY_PROVIDER_ENABLED=true$/AICRM_WECHAT_PAY_PROVIDER_ENABLED=false/' "$runtime_env" > "$disabled_runtime"
disabled_config="$test_root/disabled-config.env"
cat > "$disabled_config" <<'EOF'
AICRM_WECHAT_PAY_H5_OAUTH_ENABLED=true
AICRM_WECHAT_PAY_H5_APP_ID=wx1111111111111111
AICRM_WECHAT_PAY_H5_APP_SECRET=11111111111111111111111111111111
AICRM_WECHAT_PAY_H5_APP_SCOPE=wechat-app:wx1111111111111111
EOF
chmod 0600 "$disabled_runtime" "$disabled_config"
disabled_before="$(shasum -a 256 "$disabled_runtime")"
if AICRM_RUNTIME_ENV_FILE="$disabled_runtime" \
  "$repository_root/deploy/configure-payment-h5-oauth-runtime.sh" "$disabled_config" "$release_sha" >/dev/null 2>&1; then
  echo "H5 OAuth unexpectedly enabled without the base payment provider" >&2
  exit 1
fi
[[ "$(shasum -a 256 "$disabled_runtime")" == "$disabled_before" ]]

unset contact_key
echo "WeChat Pay H5 OAuth runtime configuration contract passed"
