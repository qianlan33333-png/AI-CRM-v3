# P5 source-authority base-diff gate

`python3 scripts/audit/check_source_authority_changes.py REPO --base BASE_COMMIT --head HEAD_COMMIT` reads the pinned Git trees and validates both source-index/lock snapshots before comparing every authority path: `web/donor-sources/source-index.json`, `web/donor-sources/source-lock.json`, and each canonical path named by either index.

No authority change passes implicitly. A reviewed change must append one record to `p5-authority-change-approvals.json` whose `base_commit` is the CI comparison base, whose `changes` is the complete sorted before/after SHA-256 list emitted by the gate, and whose `reason` and `review_reference` identify the review. The ledger does not make a coordinated index/lock/canonical edit self-validating: the gate first validates each complete snapshot, then requires the exact authority diff to match the base-bound review record.

A nonempty `review_reference` is an auditable declaration, not independent proof that a human approved the change: the ledger can be edited in the same pull request. The pull-request review process must approve the exact gate output and the declared record before merge. This gate prevents an ordinary consumer change from silently carrying a coordinated authority rewrite; it does not replace repository review or create a separate approval system.

The gate is Git-object-only and changes no working-tree path. It rejects malformed paths, incomplete JSON, index/lock disagreement, missing or mismatched canonical bytes/mode/blob identities, unapproved changes, and partial/stale approval records.
