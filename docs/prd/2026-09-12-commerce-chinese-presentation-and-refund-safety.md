# 交易与优惠券中文展示、退款确认收口

基线：`5a057e60cbd408db6dc790cad12b4d99220e23f0`。本项把管理员交易页中会误导人工判断的展示和退款关联收口为可审查的中文界面；不执行退款、不部署、不修改历史资金事实。

## 分类与边界

- **OneID**：订单只读取既有 Customer/Identity 已归属结果，显示买家姓名和面向业务定位的 `CID-<id>`；内部 canonical `customer:<id>` 只留在协议中，不直接展示。不创建客户、不猜测手机号或重新归属。
- **持久化与外部效果**：订单详情和优惠券列表是读取；退款端点仍使用既有 Payment 的事务、幂等和 External Effects 路径。本轮只把确认条件收紧到经验证的微信 `transaction_id`，不创建退款 intent、不调用 Provider。
- **历史**：`record_origin=history/v1_history` 始终只读、effect-ineligible。退款记录必须由服务端按当前订单的 Payment 关联精确筛选，不能在浏览器把全局退款列表伪装为本订单历史。

## 已核对参考与取舍

- [PR #75](https://github.com/qianlan33333-png/AI-CRM-v3/pull/75) 对历史订单采用已验证 OneID 归属、冲突隔离且禁止外部效果；本项复用既有归属读取，不扩大历史订单的写权限。
- [PR #208](https://github.com/qianlan33333-png/AI-CRM-v3/pull/208) 用 V3 Order Host 保留冻结订单渲染器，并将展示时间固定为 Asia/Shanghai；本项在该边界内补足详情投影，不改 donor。
- [PR #22](https://github.com/qianlan33333-png/AI-CRM-v3/pull/22) 规定 Coupon 经 Product 稳定 Port 取得商品选项；优惠券适用商品显示该 Port 返回的中文名称，绝不读取 Product 表或猜测 `target_ref`。

## 问题和规则

1. 订单详情分为“订单信息、支付信息、买家信息、商品与金额、退款记录”。显示买家姓名、`CID-<id>` 客户编号、订单号、脱敏手机（仅服务端已给出时）、商品中文名和付款金额；不显示重复的内部 `id`、source key、digest 或同一支付事实的多种技术别名。
2. 所有管理员可见订单、退款、优惠券状态和操作反馈使用中文文案；同名状态按订单、退款、外部处理等领域分别映射。后端 API 的 enum 和错误 code 保持原协议；前端以安全中文兜底文案显示，诊断详情不把 raw enum 直接暴露在界面。
3. 可见的时间统一固定为 Asia/Shanghai，格式严格为 `YYYY-MM-DD HH:mm:ss`，不显示 `T`、`Z`、毫秒或时区后缀；列标题不加“北京时间”。API RFC3339 和数据库 UTC 不改变。日期输入、筛选、计划时间和导出可见时间都须盘点；共享 `web/v3/adminDateTime.ts` 只将带 offset/Z 的 instant 转上海、保留纯日期日历日，且只有来源合同明确为上海壁钟时才显示无时区历史字符串。上海跨日/月边界有测试。
4. 退款确认只接受当前 Payment 已验证的微信 `transaction_id`。商户订单号、`v3pay` 订单号或任意其他订单字段都不能替代；当前记录没有可验证的 `transaction_id` 时，明确显示“缺少微信支付交易单号，不能确认退款”，并禁止提交。
5. 优惠券一级列表以 Coupon Owner 的既有 Product Port 解析 `target_refs` 为真实中文商品名，领取时间按统一格式展示，状态中文化；未知/已删除目标须可解释，不能伪造名称。

## 交付切分

- **A：订单详情与退款确认安全**：服务端按 provider + merchant order/Payment 关联筛选退款；详情渲染、退款确认、中文状态与 `adminDateTime` 上海时间；覆盖跨订单退款负例、历史只读、`transaction_id` 缺失/错误/正确以及跨日/月显示。
- **B：优惠券中文投影**：经 Product Port 的商品名、中文状态和上海领取时间；不触及 Payment 写路径。
- **后续全站接入**：从页面库存逐页接入同一最小 source-owned formatter/status mapping，覆盖显示、输入、日期筛选、计划时间和导出可见时间；不作全仓 grep 替换。

## 验收

- 订单 A 的集成测试建立两个 Payment/Order；请求一个订单只返回其退款，任何其它订单退款均不出现。
- `transaction_id_confirmation` 与已验证值不相等、为空、或以商户/v3pay 订单号替代时为 400；只有精确微信交易单号可通过该确认门，且测试不发起外部调用。
- 浏览器/adapter 测试验证分区、中文状态、客户显示、脱敏手机号、金额和退款历史只读边界；时间在 UTC 前一日/跨月时仍显示上海日期和秒。
- 优惠券测试验证真实 Product Port 名称、中文状态、无“北京时间”标题和固定上海格式；无可解析 Product 事实不伪造商品名称。
