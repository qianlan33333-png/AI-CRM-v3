# Tag catalog domain

This package is the constrained PR03 transfer of the WeCom tag-management
capability. It owns local tag groups, catalog rows, stable ordering, archive
commands, the fail-closed execution gate, and sync acceptance facts. It
deliberately does not own customer tag assignments, customer unmark
operations, provider credentials, or Provider network calls.

`app.Service` records catalog mutations through a transaction-bound receipt
and event port. `app.SyncService` records a manual or due refresh as
`queued`; the queue receipt is not a WeCom execution receipt. A later
composition change may adapt the sync queue to `outbound`, but must preserve
the `accepted -> queued -> attempted -> executed/outcome_unknown ->
reconciled` effect boundary and must never retry an unknown outcome with a new
idempotency key. `app.ExecutionStatusService` only projects the local
`provider_execution_unavailable` gate and discards opaque future/provider
fields.

Reorder commands require the complete current ID set but may change its order;
partial, duplicate, unknown, or stale memberships fail closed before a store
mutation. `SameIDs` remains the ordered comparison used to verify replayed
receipts.

The store and reference guard are interfaces only. The store must access tag
owned tables; any customer-reference check must be supplied through the
read-only `ReferenceGuard` port. No v2 package is imported and no v2 database
table is a runtime dependency.

The page entry, route/method/DTO/error contract, provider receipt lifecycle,
exclusions, source/UI inventory, and exact frozen hashes are recorded in
`docs/donor-manifests/pr03-wecom-tags.yaml` and
`docs/donor-manifests/pr03-wecom-tags.sha256`. HTTP adapters, tag-owned SQL,
and outbound Provider receipt/reconciliation remain Terra follow-ups.
