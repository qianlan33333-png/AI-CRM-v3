# CouponData 领取状态与时间展示修复

## 业务判断

管理员在 `couponData` 查看领取明细时，需要看清当前页内的领取生命周期和有效窗口，不能把服务端已经确认的 `claimed`、`valid_from`、`valid_until` 显示成原始状态码、ISO 字符串或“—”。截图证据为 `../../aicrm-artifacts/remaining-pages-chromium-20260916/desktop-evidence-74743a12a2e2/coupon-data-desktop-1280.png`：一条当前有效的 `claimed` 记录被展示为 `claimed`、当前可用 `0`、有效窗口 `—`。

`GET /api/admin/coupons/{id}/claims` 已在 Coupon Owner 的受权管理端读模型中返回 `status`、`claimed_at`、`valid_from`、`valid_until`、`redeemed_at` 和 canonical `customer_id`。此修复只消费该既有响应：

- OneID：读取已授权响应中的 canonical `customer_id`，但不解析身份、不建客、不关联跨域身份，也不显示或反查客户名称。
- Persistence：stateless；不增加持久化、事务、任务或审计写入。
- External Effects：不涉及；不调用 Provider。
- 数据边界：不补用户、商品或订单信息；缺失字段继续按冻结页面的未知展示。分页 total、401/403/5xx、重试和编辑/分享判断保持原行为。

领取状态展示只投影 Owner 已定义的 lifecycle：`claimed`/`available` 且当前在有效窗口内显示“可用”；窗口已结束显示“已过期”；`reserved`、`redeemed`、`expired`、`cancelled`分别显示其确认状态。未知状态、缺失或无效窗口不伪造成“可用”或可靠的 `0`，显示“待确认”。统计卡仍明确是“当前页”，是读取状态与窗口的展示投影，不宣称满足下单时仍需检查的商品、金额和券规则。

## GitHub 与组件参考

- GitHub 已核对冻结参考：[couponData.html](https://github.com/qianlan33333-png/AI-CRM-v3/blob/main/web/donor-sources/v2-6bfbe5816bb89913c70adaca87d6a486260e016e/web/src/admin/templates/couponData.html) 与 [controller.ts](https://github.com/qianlan33333-png/AI-CRM-v3/blob/main/web/donor-sources/v2-6bfbe5816bb89913c70adaca87d6a486260e016e/web/src/admin/controller.ts)。其五张统计卡以页面 `claims` 的中文状态计数，模板已有领取时间、有效窗口、核销时间位置。
- 当前链路为 `/admin/couponData.html` → Coupon UI/HTTP → frozen `AdminController` → `web/v3/couponAdapter.ts` → `admin_base`。复用现有 Coupon Host 和 `formatShanghaiDateTime`；不修改冻结 donor，不引入新页面壳。
- 已读取前端组件索引；其未登记独立 CouponData 组件，现有 Coupon Adapter 是唯一领域装配入口。Product Design catalog 本会话不可用，未将其结果伪作已完成。

## 实现范围

1. 在 `couponAdapter` 为同源、成功的领取明细 GET 响应补齐冻结映射所需的展示字段，同时保留所有原 canonical 字段。
2. 用既有 `formatShanghaiDateTime` 格式化确认存在的领取、有效窗口与核销时间；无效或缺失时间明确为待确认，不能输出 ISO 或伪造窗口。
3. 在 `couponData` 的 `renderVals` 展示副本中格式化券头的状态和有效期；不修改 `db` 中的 canonical lifecycle，不影响编辑判断。
4. 增加 HTTP/JSDOM 合同，覆盖当前有效、已过期、未知窗口/状态、分页和错误响应不变。

## 验收

- 一条当前有效 `claimed` 领取记录展示“可用”、上海时区秒级领取时间、完整有效窗口，当前页“当前可用”为 `1`。
- `published` 和券有效期不再显示原始状态码或裸 ISO；编辑配置仍使用 canonical state。
- 缺失/无效窗口与未知状态不显示“可用”或编造计数；用户名、商品、订单仍不被推断。
- 401/403/5xx 与分页 total/范围/重试行为不变。
- 相关 HTTP、JSDOM、TypeScript 检查通过；后续由 remaining-pages 代理在 UTF-8 PostgreSQL/Chromium 的 1280/1440 真实页面复核。
