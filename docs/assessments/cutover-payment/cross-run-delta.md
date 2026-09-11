# Payment history cross-run refresh

Classification: reuses frozen existing identity attribution; persists Payment-owned historical evidence in the caller's PostgreSQL UoW. No Identity writes, Provider reads/writes, intents, rights, or sends.

The prior importer scoped idempotency by run and inserted again on a new run. The Payment unique order/merchant constraints consequently rejected the final source delta. The Owner now locks the existing row, requires an existing history receipt, checks immutable financial/identity fields, appends a before/after source-digest and version receipt, and reuses the row. Any rejection rolls back Order delta and Payment together.

Payment terminal status is frozen. Refund historical requested/processing/failed may refresh from a newer protected source snapshot; completed/closed cannot regress. No transition uses the live refund state machine. Source timestamps cannot regress; the source normalizer currently preserves refund creation time when the source lacks a later timestamp, so distinct run and raw-row digest provide the recorded evidence.

Migration 0134 is forward-only, adds an append-only Payment-owned evidence table, and must precede the updated importer. Same-run receipt semantics remain unchanged. A new source refresh requires a new run and regenerated manifest/preconditions; old manifests are not treated as fresh provider evidence.

Validation: real PostgreSQL new-run Payment reuse, amount drift rollback, refund failed → processing → completed with three durable delta receipts, completed regression rejection, one Payment row, and zero Payment external effects. Full Payment domain, Order migration and commerce-history command tests passed with DATABASE_URL=postgresql:///postgres. No production changes.
