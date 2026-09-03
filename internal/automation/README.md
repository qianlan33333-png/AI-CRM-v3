# PR07 Automation Agent and fixed-script slice

This package is the bounded PR07 closure for Automation-owned Agent
configuration. It covers Agent/fixed-script definitions, role and task
prompts, draft/published versions, fixed content packages, copy, publish, and
the paused/archived lifecycle.

It deliberately does not implement customer automation, Customer or OneID
resolution, Segment/Audience/Campaign selection, scheduling, execution,
generation, provider calls, or outbound delivery. `AgentStatusActive` is
fail-closed in this slice; migrated configuration remains paused until a
separate execution and approval contract is reviewed.

`LegacyConfiguration` is retained as an opaque local configuration object for
donor compatibility. This slice does not interpret it as a customer,
audience, campaign, identity, or recipient binding. Any v3 adapter must keep
those relationships in their owning domains.

`port/events.go` and `port/media.go` are narrow local seams. The PostgreSQL
adapter commits configuration, idempotency receipts, audit events, and outbox
facts in one v3 UoW; this package never imports Media stores or dispatches
work. `port/effect_intent.go` freezes only local Agent/version/digest facts
for a future outbound/EER adapter and contains no recipient, prompt body,
provider payload, or credential.

The v2 agents frontend is preserved byte-for-byte under
`web/donors/automation-v2/src/`. It is archive evidence outside the v3 build.
Its raw bound-audience labels, precheck/activate operations, and broader
shared dependencies must not become PR07 routes. Production integration mounts
only a v3-owned adapter through PR10 `internal/webshell/admin_base`; the donor
page is a private template carrier and is never deployed as a second `.side`
shell or edited.
