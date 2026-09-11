# Source rule31: complete group facts and closed exclusion

OneID: resolves existing scoped WeCom identities only; no Provision, links, merges or UnionID facts. Persistence: WeCom-owned local projection and audit in a shared UoW; Provider read outside any transaction. No external write, send, retry queue or old SQL.

Migration0136 precedes use. `GroupMembershipRefresh` invalidates the old result before network I/O; process interruption, Provider failure, unresolved identity or conflict cannot preserve a usable old result. The completion uses observedAt CAS; an older in-flight request cannot overwrite a later refresh. Missing member_list, null, wrong chat, untyped/duplicate IDs fail; an explicitly empty complete member_list is valid. Raw member IDs stay in connector memory; storage contains canonical customer IDs, scope/chat reference and safe summary. Partial facts remain visible as counts but cannot be used for exclusion.

Authenticated manual endpoint: `POST /api/admin/wecom/group-membership/refresh`, JSON `{"chat_reference":"PROTECTED_CONFIGURED_GROUP"}`. Requires normal superadmin session and CSRF, plus existing ChannelProviderReadEnabled; no permission bypass or query-string identity. Returns only completion, observation time and counts. It reads Provider and writes local facts; does not send anything.

Template `member_excluding_group_paid` parameters:
- owner_scope: all; owner_staff_ids: []
- exclude_group_chat: the exact reviewed same-Corp source group reference
- excluded_product_codes: SOAK-FDE-BUNDLE-S1, FDE-CAMP-S1, SOAK-QTR

The closed evaluator selects current proven HXC active memberships with a recognized WeCom contact; excludes canonical IDs in the complete group snapshot and canonical payers of the three paid products. Uses existing HXC, WeCom and Order ports only. No arbitrary boolean DSL or SQL. The group snapshot must be complete, no unresolved members, observed not in the future and <=15 minutes old. Missing data fails refresh rather than broadening the audience.

Current limit: group refresh is manual. Enabling a scheduled audience does not automatically refresh its group facts; after15 minutes evaluations stop safely. Production always-on scheduling requires wiring the bounded refresh service through the existing jobqueue before scheduled audience evaluation; no such recurring wiring is claimed here. Real group Provider read and production availability are not yet verified. API configuration/preview can be tested in isolation after the manual refresh.

Validation: local real PostgreSQL complete-empty, scope mismatch, missing/future/stale, inflight invalidation, partial identity, old writer CAS; Provider protocol malformed responses; no network inside UoW; admin/CSRF boundary; group and paid exclusions; all targeted race tests and architecture gate passed. No production writes.
