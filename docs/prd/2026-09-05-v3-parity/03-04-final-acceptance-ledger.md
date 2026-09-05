# PRD-03/04 最终验收核账

核对日期：2026-09-06（Asia/Shanghai）
实现 HEAD：`270d8f986342138aec384a58588ff91feae227b9`（PR #143 分支）
总控依据：`v3-parity-integration` 工作区当日的 `03-product-payment.md`、`04-entitlement-coupon.md`、`10-acceptance-matrix.md`。该工作区正在机械整合，本文不把它的未提交状态当作本分支的实现事实。

## 范围与判定

本文只核对 PRD-03/04 的已批准能力和最终验收门槛，不新增产品设计，也不将历史退回项重复登记为待开发。结论分为三类：

- **已有证据**：冻结供体、实际 V3 入口和准确测试三者可对应；不作为当前缺口。
- **最终组合门槛**：实现已在本分支，仍须由总控把准确 HEAD 纳入组合并跑组合 CI；不等同于功能未实现。
- **真实剩余风险**：尚不能安全声称满足旧合同的事项。

OneID：读取既有 canonical customer、可信 Payment OAuth 会话和既有 Identity 历史解析；没有新增身份匹配、隐式建客或客户副本。
持久化/效果：订单、券、权益、Payment 的业务事实分别由 Owner 在 PostgreSQL UoW 中写入；本轮签名支付只使用本地模拟 Provider，未发起生产付款、退款或 OAuth 启用。

本分支的完整 CI `33975881872` 在 HEAD `270d8f9` 成功完成 repository PostgreSQL 检查和 race-check；deploy 为 skipped。此前 `06ecac29f3d872ba57902ef7e4a29e475fad7d2f` 的 CI `33973228872` 是已获总控审核的 03/04 联合及历史基线。本文未在本机重跑 PG：本机没有可用测试数据库，集成测试会跳过，故不把本地跳过写成通过。

## 已有证据（不列为缺口）

| PRD 能力 | 冻结旧来源 | V3 实现及准确测试 | 结论 |
|---|---|---|---|
| 周期商品创建、编辑、复制、上下架、精确公开 code 与分享路由 | `service_period/templates/service_period_products.html`、`service_period/application.py`、`public_product` | `internal/product/app/service_period_test.go`: `TestServicePeriodCreateReplayAndPayloadConflict`、`TestServicePeriodCASCopyEnableDisableArchiveAndReferenceRetention`、`TestServicePeriodPublicReaderUsesExactEnabledCode`；普通商品生命周期亦由 `local_lifecycle_test.go` 的 `TestLocalProductLifecycle*` 覆盖 | 已有可见动作、CAS/收据和精确 code 证据；没有把数字 ID 公开路由当作周期商品。 |
| 周期公开页的冻结模板、图片、二维码、状态刷新、CTA、上海到期日及可信会话读取 | `service_period_public.py` 与 `public_product/service.py` 的渲染函数/详情媒体片段 | `internal/product/http/public_test.go`: `TestFrozenServicePeriodFStringDecodesOnlyStaticLiteralSegments`、`TestFrozenServicePeriodPublicBrowserJourney`、`TestPublicServicePeriodRendersTrustedEntitlementWithoutIdentityFallback`、`TestPublicServicePeriodStateEndpointUsesTheFrozenStateContract`、`TestPublicServicePeriodMediaIsRestrictedToProductSlices` | 已修复 Python 静态字面量解码，JSDOM 执行实际 Host；动态内容不会再被二次模板替换。 |
| 会员表的原入口、保存视图、协作、可撤销分享、备注及员工权限 | `service_period_member_grid{,_compact_base,_public}.html`；`member_grid.js`、`member_grid_state.js`、`member_grid_share.js`、`member_grid.css` | `cmd/aicrm/member_grid_route_test.go`: `TestProductDataEntryMountsEmbeddedMemberGridAndAssets`；`internal/product/http/handler_test.go`: `TestFrozenMemberGridHTTPAPISavedViewCollaboratorShareAndRemarkJourney`、`TestReadOnlyMemberGridCollaboratorGetsExplicitForbiddenAndNoWrites`、`TestFrozenMemberGridBrowserJourneyUsesActualHTTPAPI`、`TestMemberGridPublicHttpAPIOnlyReadsEnabledShareAndSavedViews`；`internal/product/store/member_grid_integration_test.go`: `TestMemberGridPostgreSQLCRUDAndCAS` | 冻结页面由嵌入制品发布，真实 API/JSDOM 行程覆盖读写、撤销后拒绝及公开分享，不依赖冻结的 `web/src/api/admin.ts`。 |
| 全量会员集合的 20 条筛选、8 排序、2 分组、跨 Owner 组合和稳定分页 | `member_grid.py`（`MAX_FILTER_CONDITIONS=20`、`MAX_SORTS=8`、`MAX_GROUPS=2`）及 `member_grid_repo.py` | `internal/product/http/member_grid_composition_test.go`: `TestMemberGridCompositionAppliesFullRelationAcrossOwnersBeforePaging`、`TestMemberGridCompositionPinsHXCGenerationAndRejectsPrunedCursor`、`TestMemberGridCompositionRejectsCursorWhenHXCGenerationAdvances`、`TestMemberGridCompositionProbesCandidateLimitBeforeFiltering`、`TestMemberGridCompositionBatchesCustomerAndHXCReads`、`TestMemberGridCompositionKeepsUnavailableDistinctFromKnownEmptyAndZero`、`TestMemberGridCompositionDropsPartialHXCFirstRead`、`TestMemberGridCompositionDoesNotUseDisplayPlaceholderAsCustomerName`、`TestMemberGridHTTPReturnsExplicitCursorStale`；Order PG: `TestPostgreSQLMemberGridUsesFrozenCeilDaysForFiltersAndPagination`、`TestPostgreSQLMemberGridAppliesMultipleOrderFactsBeforeStablePaging`、`TestPostgreSQLMemberGridRenewalCountUsesVerifiedOrderSources` | Product 只读编排完整候选关系，限制 10,000 并探测第 10,001 条；游标绑定 HXC 代与关系摘要，unknown 不被伪造成 false/0/空字符串。 |
| 付款恢复：丢失创建响应、同键恢复、会话变化、终态读取、取消/未知/本地存储失败 | `public_product/service.py` 的 checkout 前端和 Payment 旧协议 | `cmd/aicrm/public_checkout_journey_test.go`: `TestPublicCheckoutBrowserJourney`；`cmd/aicrm/public_checkout_recovery_postgres_integration_test.go`: `TestPostgreSQLPublicCheckoutResponseLossRejectsRenewedSessionReplay`；`internal/payment/app/service_test.go`: `TestExistingOrderRejectsLegacySessionNewPaymentButAllowsExactReplay`、`TestGetCheckoutAllowsRenewedSamePayerToReadTerminalWithoutHandoff` | 浏览器实际走 public/Payment HTTP；关键的响应丢失与新 OAuth 会话由真实 Payment/Order PostgreSQL Store 证明在 Create 前拒绝，已知原单可按可信付款人只读恢复。 |
| 公开券领取、可用券查询、自动最佳券、预占/核销/关闭释放与券页面 | `commerce/coupons/public_api.py`、`coupon_public.html`、`coupon/domain.py`、`coupon/repo.py` | `internal/coupon/http/public_test.go`: `TestPublicCouponUsesTrustedSessionForClaimAndAvailableCoupons`、`TestFrozenCouponPublicTemplateRetainsDonorDOMAndRejectsClaimFields`、`TestFrozenCouponPublicPageRendersLimitAndTerminalStates`、`TestPublicCouponOAuthAndMalformedRoutesFailClosed`；`internal/coupon/store/postgres_integration_test.go`: `TestPostgreSQLCouponCheckoutConcurrentClaimReservationAndSettlement`；`cmd/aicrm/commerce_checkout_integration_test.go`: `TestPostgreSQLCommerceNoCouponReplayRetainsCheckoutPrice` | 不接收浏览器 CustomerID/价格；上海自然日、并发末券、同键跨时刻、payload 漂移、过期后关闭均有 Owner PG 覆盖。 |
| 03/04 签名支付→券核销→权益开通/续期→部分退款→重复/并发退款 | `service_period/payment_consumer.py`、`refund_consumer.py`、`coupon/repo.py`；PRD-04 §6 冻结日期和退款规则 | `cmd/aicrm/commerce_funds_http_integration_test.go`: `TestPostgreSQLCommerceFundsHTTPJourney`；`internal/order/store/postgres_integration_test.go`: `TestPostgreSQLServicePeriodFulfillmentKeepsLegacyCoverageAndRevokesOnce`、`TestPostgreSQLServicePeriodRefundKeepsOnlyIndependentHistory`、`TestPostgreSQLServicePeriodRefundRevokesMappedHistoryAndNeverReclassifiesExpiredHistory` | 该真实 PG+HTTP 行程覆盖验签拒绝、乱序、权益故障整笔回滚、并发回调只核销/开通一次、首次部分退款一次扣期、第二退款不重复扣期和超额退款冲突。它是本地签名 Provider，不是生产支付。 |
| 历史订单、支付、退款的冻结 manifest apply→重放→逐条 reconcile | `docs/07-PRD-交易管理与历史用户数据迁移.md` 对应的旧 commerce 订单/支付/退款快照 | `cmd/migrate-commerce-history/main_postgres_integration_test.go`: `TestPostgreSQLCommerceHistoryCommandApplyReplayReconcile`；各 Owner 逐条核验：`internal/identity/migration/reconcile_integration_test.go`、`internal/order/migration/reconcile_integration_test.go`、`internal/payment/migration/reconcile_integration_test.go` | 实际 Runner 编排 Identity/Order/Payment；failpoint 验证 Owner UoW 回滚，重放不新建效果，保留源 digest 而目标漂移会拒绝 reconcile。 |
| 历史周期权益和券定义/持券事实 | `service_period_entitlements`、`commerce_coupon_claims`；`commerce_coupons` 与绑定表 | `cmd/migrate-sidebar-history/main_postgres_integration_test.go`: `TestPostgreSQLSidebarHistoryAllianceApplyReplayReconcile`、`TestPostgreSQLSidebarHistoryLegacyNoAllianceDigestReplayAndQuarantine`；券定义由 `internal/configmigration/source/extract.go`/`target/runner.go` 的 `commerce_coupons` 映射及 `cmd/migrate-v2-config-definitions/integration_test.go` 覆盖 | 周期权益、券领取/核销行与券定义由各自 Owner 的历史工具处理；未知身份隔离，不通过在线支付、领券或开通流程补造事实。 |
| 0088 联盟的 unknown/明确空/值、编辑、回读、回放、目标漂移和发布清单 | `ServicePeriodRepository._member_payload` 与 `member_admin_fields.py` 的 `metadata_json.admin_alliance`；`member_grid.py` 的联盟列 | `internal/order/store/postgres_integration_test.go`: `TestEntitlementAlliancePreservesUnknownClearCASAndRollback`；上列 member-grid HTTP/JSDOM 行程；两项 `cmd/migrate-sidebar-history` PG 测试；`scripts/check-install-release-contract.sh` 与 `scripts/test-install-release-ordering.sh` 在 CI 执行 | `0088_order_service_entitlement_alliance.sql` 及 Release readiness 已在 `270d8f9`；旧快照缺字段保持旧 canonical digest，mapped 与 quarantined 行均逐条对账。 |

## 真实剩余风险与最终门槛

1. **0088 尚未进入新的总控组合 HEAD/组合 CI。** `270d8f9` 的源 CI 已通过，但总控工作区尚处于整合冲突处理状态。它是最终交付门槛，不是本分支尚缺的联盟、会员表或支付实现。纳入后应以包含该 SHA 的新组合运行验证；不能拿 `11c1c69` 或 `df2dfd4` 的旧组合 CI 代替。

2. **首次真实侧栏历史快照的 alliance 空白字符合同尚未精确证明。** `scripts/capture-sidebar-history-source.sh` 第 39–42 行声称 PostgreSQL `BTRIM` 与 Python `str(...).strip()` 相同，这不成立：默认 `BTRIM` 只移除 ASCII space，Python/Go 的 `strip`/`TrimSpace` 还会移除 tab 和 Unicode 空白。冻结旧写路径是 `api.py` → `application.py` → `domain.text`，其中 `text(value)` 为 `str(value or "").strip()`，并在 `member_admin_fields.py` 入库；`member_grid.py:704` 读模型也再次 Python `strip()`。因此经旧 HTTP 编辑的正常值通常已经规范化，风险局限于早期/直写数据或非 ASCII 外侧空白；但在没有真实只读快照样本前，不能把 `BTRIM` 的注释称为精确等价。

   在首次生产源提取前，应冻结下列一个明确输入合同并补测试向量：`" 联盟 "`、`"\\t联盟\\t"`、`"\\u00a0联盟\\u00a0"`、明确空字符串、字段缺失、JSON 非 string。若迁入目标必须保存旧页面的可编辑值，应明确采用旧 HTTP 写路径的 Unicode `strip` 规范化并把“原始值→规范值”记录为受审转换；若要保留原始证据，则源摘要和目标事实不能再以同一字符串直接比较。此项无需改产品页面或身份设计，但在确认前不应运行真实 capture/apply。

3. **生产 Provider/生产 OAuth 与真实历史数据库执行仍是刻意保留的上线阶段。** PRD-03/04 明确要求本轮只跑隔离 PG 与本地签名 Provider；CI deploy 均 skipped。它们不是本轮待补的开发能力，不能被当前测试宣称为已生产验收。

除以上三项，本次核查没有发现仍未由准确实现、冻结来源和实际测试共同覆盖的 PRD-03/04 退回能力。特别是旧的空会员表、公开页字符串替换、保存 view 不生效、协作撤销后仍可写、未知支付换键、资金链未验签、退款重复扣期、历史只计数不逐行核验，均已有上述证据，故不重复列为待开发。
