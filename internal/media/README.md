# Media migration preparation (PR02)

This package is the bounded PR02 Media capability, derived from a frozen
legacy donor. It contains Media-owned validation, PostgreSQL persistence,
opaque reference protection, compatibility HTTP routes, and
provider-independent image/attachment/card operations.

The package is registered only through the v3 composition root and uses the
single PostgreSQL UoW for resources, receipts, audit events, and outbox rows.
It imports neither the v2 module/runtime/database nor other domains' internal
layers. Provider calls remain disabled; content packages are owned by PR06 and
are not exposed by the PR02 routes.

The exact v2 admin templates and their existing TypeScript/CSS/shared API
dependencies are staged under `web/donors/media-v2/`. They remain byte-exact
and are mounted only inside the v3 `admin_base` shell. Their source and
SHA-256 reconciliation are recorded in
`docs/migration/media/pr02-donor-manifest.yaml` and
`docs/migration/media/pr02-donor-sha256.txt`.
