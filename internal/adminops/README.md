# PR09 Admin Ops slice

This package freezes the local Admin Ops control-plane contracts and leaf
behavior: credential references and safe metadata validation, configuration
category/release/job mutation semantics, safe historical projections, and
read-only execution-runtime normalization/redaction. Secret material is never
accepted as a value; only opaque references and masks may cross the port.

`port.Repository` is intentionally a v3-local persistence seam. Terra must
provide the Admin Ops-owned store, transactional idempotency receipts, audit,
and authenticated HTTP/OpenAPI adapters later. This slice has no generated
SQL, migration, Composition Root registration, worker execution, customer or
identity repair, or Provider call.
