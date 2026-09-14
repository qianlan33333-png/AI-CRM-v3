# HTTP contract gap closure: G02-G14

Candidate SHA: `dcfc02290daca3f4bb9509120698e9e976b5369b`.

## Business decision

Release acceptance needs evidence that each mounted HTTP route reaches its application contract with the correct access boundary, validated input, and error mapping. The scope is tests only: no customer identity is read, resolved, provisioned, or linked; no database, durable job, Provider read, or Provider write is performed. In-memory fakes expose the handler-to-service command while preventing network and production access.

## Reference check

The Go standard library `net/http/httptest` pattern is used, consistent with the existing package tests. GitHub's Go transport documentation also treats `Idempotency-Key` as a request-idempotency signal; route tests therefore assert that the handler forwards the exact one accepted key instead of merely asserting a success code. No external framework is adopted.

## Acceptance scope

Cover G02/G03, G05/G06, and G08-G14 with each route's actual method and path: successful response plus forwarded command/query data, and at least one authorization, CSRF/service-token, malformed request, not-found/conflict, or dependency-error boundary as appropriate. Tests must call no real Provider or database. G15 and G16 remain out of scope because their issue is mount/document parity, not an HTTP-test gap.

## Architecture classification

OneID: not involved — these routes operate tag configuration, acquisition-link commands, and operation-cycle records, and the test fakes never accept an external identity.

Persistence: stateless test harness — fakes stand in for local transactions and durable-effect acceptance; no state is persisted and no external effect is emitted.
