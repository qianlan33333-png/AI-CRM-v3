# PR05 coupon rule slice

This package is the bounded Coupon-domain preparation for PR05. It owns local
coupon rule definitions and their rule-management behavior: create, edit,
copy, publish, stop, archive, safe draft deletion, product applicability,
validity windows, and rule-owned issue counters/statistics projection.

The slice deliberately has no claim execution, redemption, customer-held
coupon, order, payment, entitlement, membership, or OneID operation. It does
not copy the v2 coupon history or store layer and has no v2 runtime, database,
module, migration, or provider dependency.

`port/target.go` is a narrow temporary compatibility port for checking the
price/currency of a standard-product target before publication. Terra should
adapt it to the canonical Product port at the composition boundary; Coupon
must not import Product app/store/http packages or read Product tables.

`port/events.go` is a local event seam that preserves rule mutation facts
without dispatching work. Terra must adapt it to the v3 versioned event and
outbox ports in the integration lane.

The raw frontend donor is staged under
`web/donors/coupons-v2/src/`. Those files are immutable byte-exact evidence,
outside the build tree, and must not be edited. Some raw shared/controller and
coupon-data code mentions excluded claim/history behavior; the backend
compatibility boundary must keep those routes absent or feature-gated rather
than expanding this PR05 slice.
