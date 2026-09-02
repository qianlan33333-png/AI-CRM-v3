# Operation-cycle domain

This package is the constrained PR08 transfer of the operation-cycle
capability. It keeps the local contracts for strategy definitions and
versions, run snapshots, runner compatibility, action stages, enable/pause/
archive state, proposals, and transaction-bound local facts.

The domain intentionally carries no customer, external-user, segment,
campaign, recipient, tenant, order, product, membership, subscription,
entitlement, service-period, credential, or Provider identity. `app.Service`
records local lifecycle facts through the operation-cycle event and delivery
ports; it does not call a Provider, enqueue a campaign recipient, or retry an
`outcome_unknown` effect. `domain` validation fails closed when excluded
identifiers or external-effect payloads appear in a snapshot, result,
proposal, or context filter.

The Store, HTTP adapter, runner wiring, PostgreSQL migration, generated
queries, and historical observation/static-history readers remain deferred to
Terra. They must preserve the local `accepted` fact boundary and add any
outbound integration only through the versioned outbound contract.

The frozen donor inventory and exact UI hashes are recorded in
`docs/donor-manifests/pr08-operation-cycles.yaml` and
`docs/donor-manifests/pr08-operation-cycles.sha256`; raw templates are under
`web/donors/operation-cycles-v2` and are not wired by this commit.
