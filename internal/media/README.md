# Media migration preparation (PR02)

This package is the bounded PR02 donor slice from the frozen legacy
repository. It contains reusable Media-owned validation, value objects,
cross-domain ports, Group Ops material freezing, and provider-independent
image read/variant and content-package application logic.

The package deliberately does not register itself in `cmd/aicrm`, add a
migration, or import the v2 module/runtime/database. `app/service.go` is a
v3-local read-side composition adapter; transactional stores, receipts,
events, external effects, and HTTP wiring remain integration work for the
Media owner/Terra.

The exact v2 admin templates and their existing TypeScript/CSS/shared API
dependencies are staged under `web/donors/media-v2/`. That directory is a
byte-exact donor snapshot only and is not part of the v3 frontend build.
Its source and SHA-256 reconciliation are recorded in
`docs/migration/media/pr02-donor-manifest.yaml` and
`docs/migration/media/pr02-donor-sha256.txt`.
