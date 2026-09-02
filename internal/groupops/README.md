# Group Ops migration preparation (PR06)

This package is the bounded Group Ops donor slice. It keeps local plan
definitions, staff membership references, opaque group bindings, ordered
message/delay nodes, webhook descriptor metadata, schedule/run/receipt and
reconciliation contracts, and transaction-bound draft lifecycle behavior.

The package deliberately carries no customer, OneID, segment, audience,
Campaign, recipient, or customer-marking operation. `DispatchProvider` is a
contract only; no implementation here calls WeCom or another Provider. A
Provider write, if later approved, must be accepted and reconciled through the
versioned `outbound` boundary with `accepted`, `attempted`,
`outcome_unknown`, and delivery-evidence states kept distinct.

Content-package creation, validation, versioning, and Media-owned material
freezing remain owned by the PR02 Media slice. Group Ops stores typed opaque
material references and consumes a future Media snapshot port; it does not
read Media tables directly or duplicate Media's owner files.

The Store, PostgreSQL schema, migration, generated queries, HTTP/auth and
composition wiring, runtime worker, historical importer, and Provider
adapter remain deferred to Terra. The exact donor frontend files are staged
under `web/donors/groupops-v2/` only and are not part of the v3 build.
Production integration must mount them through the PR10 v3-owned webshell and
`admin_base`; this archive is not a second deployable v2 shell and must not
introduce a second `.side` navigation.

Source and target SHA-256 reconciliation is recorded in
`docs/migration/groupops/pr06-donor-sha256.txt`; the inventory and deferred
work are recorded in `docs/migration/groupops/pr06-donor-manifest.yaml`.
