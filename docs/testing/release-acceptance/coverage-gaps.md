# Release acceptance coverage gaps

Candidate: `dcfc02290daca3f4bb9509120698e9e976b5369b`
Source root: `/private/tmp/aicrm-release-test-source-dcfc022`
Source HEAD/tree: `dcfc02290daca3f4bb9509120698e9e976b5369b` / `0d2396be8d3ccbec9985fe0b9a5c01613d0e5ec7`
Tracked diff clean: `True`
Output directory: `/private/tmp/aicrm-release-acceptance-dcfc022/docs/testing/release-acceptance`
Generated: `2026-09-14T02:07:20.747672+00:00`

本报告是候选版本的静态覆盖盘点，不是测试通过报告。OpenAPI method+path、源码静态挂载、路由字符串线索和测试文件文本引用均只表示存在证据入口；路由字符串线索不是实际挂载证明，测试引用未核对 HTTP method 或断言。所有 `release_proof` 保持 `not_proven`，直到在隔离环境中执行候选 SHA 并回读业务/Provider 结果。

扫描到 `520` 个 OpenAPI 操作、`428` 个路径、`605` 个测试文件。端点状态统计：`{"module_text_reference_only": 36, "mount_not_found_by_static_scan": 1, "mounted_no_text_reference": 13, "route_clue_without_mount": 2, "text_reference_candidate": 468}`。

## 立即补测缺口

### U1 · P0 · transactions/payment/refund

Money movement and outcome_unknown paths must be proven before any release decision.

源代码/合同证据：`api/openapi.yaml (Transactions operations)`; `cmd/aicrm/composition.go:2310-2319 (payment/refund mounts)`。

已有测试映射：`cmd/aicrm/commerce_checkout_integration_test.go`; `cmd/aicrm/commerce_funds_http_integration_test.go`; `internal/payment/app/service_refund_postgres_integration_test.go`; `internal/payment/http/handler_test.go`; `internal/payment/store/postgres_integration_test.go`。这些文件尚未被本盘点当作当前候选的通过收据。

缺口：Inventory maps routes/tests but has no candidate-bound run receipt proving callback replay, timeout/outcome_unknown, refund idempotency, reconciliation and post-state readback.

建议用例：Isolated PostgreSQL/provider protocol: create checkout -> signed payment callback replay/乱序 -> outcome_unknown -> original-key lookup/reconcile -> refund -> order, entitlement and ledger readback; assert no duplicate external effect.

### U2 · P0 · distribution

The frozen SHA introduces the first-level referral lifecycle; qualification, commission and refund settlement are release-critical.

源代码/合同证据：`internal/distribution/http/handler.go:114-130`; `cmd/aicrm/distribution_routes.go:34-36`; `docs/contracts/2026-09-14-first-level-distribution.md`。

已有测试映射：`internal/distribution/http/handler_test.go`; `internal/distribution/http/admin_test.go`; `internal/distribution/domain/distribution_test.go`; `cmd/aicrm/distribution_settlement_refund_postgres_integration_test.go`。这些文件尚未被本盘点当作当前候选的通过收据。

缺口：Existing tests are mapped, but this inventory cannot prove the complete user journey on dcfc022: registration/attribution -> paid order -> refund adjustment -> due/settled commission -> reconciliation and payer/beneficiary separation.

建议用例：Run a candidate-bound isolated journey with two customers, duplicate referral links, paid order, partial/full refund, due worker restart and reconciliation; verify commission exactly once and correct customer roots.

### U3 · P0 · OneID/identity and access

Identity misattribution or role bypass blocks release even when route/unit tests pass.

源代码/合同证据：`internal/identity/http/handler.go:175-370`; `cmd/aicrm/composition.go:2276-2281`; `internal/access/http`。

已有测试映射：`internal/identity/http/handler_test.go`; `cmd/aicrm/access_governance_ui_chromium_postgres_integration_test.go`; `cmd/aicrm/customer_sync_integration_test.go`。这些文件尚未被本盘点当作当前候选的通过收据。

缺口：No evidence in this inventory establishes the candidate run across pending/conflict/verified identity, cross-customer rejection, role migration, server enforcement, browser visibility and super-admin-only security configuration.

建议用例：Use isolated fixtures for viewer/admin/super-admin plus two customer roots: assert denied cross-root access, unresolved identities remain pending/conflict, role demotion takes effect in API and browser, and only super-admin can change security config.

### U4 · P0 · external effects/outcome_unknown

Group, channel welcome, automation and AI writes share the outbound/effects reliability boundary.

源代码/合同证据：`internal/externaleffects/port`; `cmd/aicrm/composition.go:2323-2325`; `cmd/aicrm/composition.go:1945-1947`。

已有测试映射：`cmd/aicrm/group_ai_joint_runtime_integration_test.go`; `cmd/aicrm/group_ops_runtime_integration_test.go`; `web/scripts/outbound-task-history-e2e.mjs`。这些文件尚未被本盘点当作当前候选的通过收据。

缺口：Existing mappings do not constitute provider receipt proof. Need one candidate run covering accepted/queued/attempted/executed/outcome_unknown/reconciled, retry with original idempotency key, worker restart and no duplicate provider effect.

建议用例：Run local provider protocol with deliberate timeout after provider acceptance, restart worker, reconcile by original effect id, and read back business receipt plus provider result for channel/group/AI representative operations.

### U5 · P1 · release migration/readiness

A clean source tree and green unit tests do not prove the release package can migrate, start, recover and expose the candidate SHA.

源代码/合同证据：`cmd/aicrm/readiness_integration_test.go`; `deploy/install-release.sh`; `migrations/`。

已有测试映射：`cmd/aicrm/readiness_integration_test.go`; `scripts/test-install-release-ordering.sh`; `scripts/check-install-release-contract.sh`。这些文件尚未被本盘点当作当前候选的通过收据。

缺口：No candidate-bound package/migration/backup-restore receipt is included; readiness and deploy scripts remain separate from authenticated browser/provider acceptance.

建议用例：Build exact dcfc022 package in isolated PostgreSQL16, run new/upgrade/interrupted migration and restore rehearsal, assert readyz/version/critical routes, then retain rollback evidence.

## 关键能力覆盖

| 能力 | 角色维度 | 状态维度 | 测试映射 | 结论 |
|---|---|---|---|---|
| access-and-role-governance | unauthenticated, viewer, admin, super-admin | active, disabled, role changed, CSRF rejected, session expired | cmd/aicrm/access_governance_ui_chromium_postgres_integration_test.go；cmd/aicrm/access_login_fixture_schema_integration_test.go；cmd/aicrm/adapters_test.go；cmd/aicrm/admin_access_journey_integration_test.go | `text_reference_candidate_not_run` / `not_proven` |
| oneid-and-customer-identity | admin, viewer, provider-verified adapter | pending, verified, conflict, merge candidate, merged, reversed | cmd/aicrm/access_governance_ui_chromium_postgres_integration_test.go；cmd/aicrm/admin_access_journey_integration_test.go；cmd/aicrm/admin_shell_layout_chromium_postgres_integration_test.go；cmd/aicrm/aiassistant_http_runtime_integration_test.go | `text_reference_candidate_not_run` / `not_proven` |
| customer-directory-and-sync | viewer, admin, super-admin | queued, running, completed, failed, partial/unresolved | cmd/aicrm/access_governance_ui_chromium_postgres_integration_test.go；cmd/aicrm/admin_shell_layout_chromium_postgres_integration_test.go；cmd/aicrm/aiassistant_http_runtime_integration_test.go；cmd/aicrm/aiassistant_identity_integration_test.go | `text_reference_candidate_not_run` / `not_proven` |
| wecom-channel-acquisition | viewer, admin, super-admin, provider callback | draft, active, archived, accepted, executed, receipt unknown, reconciled | cmd/aicrm/access_governance_ui_chromium_postgres_integration_test.go；cmd/aicrm/adapters_test.go；cmd/aicrm/aiassistant_http_runtime_integration_test.go；cmd/aicrm/audience_channel_radar_reference_test.go | `text_reference_candidate_not_run` / `not_proven` |
| transactions-payment-refund | public buyer, admin, finance operator (actual Access role mapping required), provider callback | created, pending, paid, failed, outcome_unknown, refunded, reconciled | cmd/aicrm/access_governance_ui_chromium_postgres_integration_test.go；cmd/aicrm/admin_access_journey_integration_test.go；cmd/aicrm/admin_shell_layout_chromium_postgres_integration_test.go；cmd/aicrm/audience_group_refresh_test.go | `text_reference_candidate_not_run` / `not_proven` |
| first-level-distribution | customer/distributor, admin, finance operator (actual Access role mapping required) | eligible, attributed, pending, due, paid, refunded, exception, reconciled | cmd/aicrm/distribution_chromium_postgres_integration_test.go；cmd/aicrm/distribution_product_policy_chromium_postgres_integration_test.go；cmd/aicrm/distribution_product_policy_postgres_integration_test.go；cmd/aicrm/distribution_settlement_refund_postgres_integration_test.go | `text_reference_candidate_not_run` / `not_proven` |
| products-entitlements-coupons | public buyer, admin, viewer | draft, enabled, disabled, claimed, reserved, redeemed, released, expired | cmd/aicrm/access_governance_ui_chromium_postgres_integration_test.go；cmd/aicrm/access_login_fixture_schema_integration_test.go；cmd/aicrm/admin_access_journey_integration_test.go；cmd/aicrm/admin_shell_layout_chromium_postgres_integration_test.go | `text_reference_candidate_not_run` / `not_proven` |
| group-operations-and-outbound | viewer, operator (actual Access role mapping required), admin, provider directory | draft, paused, running, accepted, executed, outcome_unknown, reconciled | cmd/aicrm/admin_access_journey_integration_test.go；cmd/aicrm/aiassistant_http_runtime_integration_test.go；cmd/aicrm/aiassistant_media_preparation_test.go；cmd/aicrm/channel_archived_entrant_integration_test.go | `text_reference_candidate_not_run` / `not_proven` |
| automation-and-ai-approval | viewer, operator (actual Access role mapping required), admin, super-admin | draft, prechecked, pending review, approved, rejected, queued, unknown, reconciled | cmd/aicrm/aiassistant_http_runtime_integration_test.go；cmd/aicrm/aiassistant_identity_integration_test.go；cmd/aicrm/aiassistant_media_preparation_test.go；cmd/aicrm/audience_owner_uow_integration_test.go | `text_reference_candidate_not_run` / `not_proven` |
| survey-and-public-h5 | admin, viewer, public respondent, OAuth verified | draft, published, disabled, submitted, unresolved, external effect unknown | cmd/aicrm/admin_access_journey_integration_test.go；cmd/aicrm/audience_survey_reference_test.go；cmd/aicrm/customer_profile_adapters_test.go；cmd/aicrm/distribution_chromium_postgres_integration_test.go | `text_reference_candidate_not_run` / `not_proven` |
| media-and-preparation | admin, viewer, provider credential | local, prepared, queued, succeeded, failed, outcome_unknown, expired | cmd/aicrm/admin_shell_layout_chromium_postgres_integration_test.go；cmd/aicrm/aiassistant_media_preparation_test.go；cmd/aicrm/audience_group_refresh_test.go；cmd/aicrm/audience_rule_activation_test.go | `text_reference_candidate_not_run` / `not_proven` |
| open-platform-and-api-docs | open-platform client, super-admin, admin | created, active, disabled, rotated, revoked, audit read | cmd/aicrm/adapters_test.go；cmd/aicrm/customer_owner_handoff_chromium_postgres_integration_test.go；cmd/aicrm/excel_batches_integration_test.go；cmd/aicrm/group_ops_protocol_auth_test.go | `text_reference_candidate_not_run` / `not_proven` |
| runtime-config-release-and-readiness | viewer, admin, super-admin | draft, validated, published, rolled back, blocked, ready, unready | cmd/aicrm/access_governance_ui_chromium_postgres_integration_test.go；cmd/aicrm/access_login_fixture_schema_integration_test.go；cmd/aicrm/admin_access_journey_integration_test.go；cmd/aicrm/admin_shell_layout_chromium_postgres_integration_test.go | `text_reference_candidate_not_run` / `not_proven` |

## 端点状态解释

- `text_reference_candidate`：至少一个测试文件按路径前缀或 operation id 文本命中；未核对 HTTP method/断言，未执行、未绑定候选 SHA、未证明业务或 Provider 结果。
- `module_text_reference_only`：只有宽泛模块测试文本引用，没有直接引用该 method/path 家族；需要补具体端点或 Journey。
- `mounted_no_text_reference`：源码有静态 Go 挂载证据，但未找到测试文件文本引用。
- `route_clue_without_mount`：存在源码/前端路由字符串线索，但没有静态 Go 挂载证据；线索不等于运行时不可达，需实际 HTTP 检查。
- `mount_not_found_by_static_scan`：静态扫描没有找到 Go 挂载或路由字符串线索；这不单独证明运行时缺陷，需核对隐式 wrapper、生成路由或 stale OpenAPI。
- 能力表中的 `finance operator`、`operator` 等角色是测试维度标签，必须映射到真实 Access role 后再判断权限覆盖。

以下边界保持不变：Provider 配置、入队、HTTP 200、accepted/queued/executed、静态截图和 Mock 都不能替代实际接收/到账/交付或 reconciliation 证据；身份不确定时必须保持 pending/conflict。
