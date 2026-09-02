#!/usr/bin/env bash
set -euo pipefail

check() {
  local expected="$1" file="$2"
  local actual
  actual="$(shasum -a 256 "$file" | awk '{print $1}')"
  test "$actual" = "$expected" || { echo "PR01 donor checksum mismatch: $file" >&2; exit 1; }
}

check 183e85bdc911c7456a61da81cbbefd021577cc03813f8dce02408ce46896555c web/src/admin/sections/campaigns.ts
check 8646e9534ca331a107fb2afe80cbb0ec9c50c999a1f8ca6dd57c6be926b6683e web/src/api/external_effects.ts
check 333cc2e1e7309ec751c56db402ce1abed0f10bcb859d92dabe84da64065b1ef2 web/src/admin/sections/observability.ts
