# 公开会员表未知总数呈现 PRD

日期：2026-09-16  
状态：已授权实施；修复公开只读会员表的错误行数文案。

公开 `/shared/service-period-member-grid` 的既有 Product HTTP query 有行和游标，却刻意不承诺全量 `total`。冻结 dd8 renderer 把其内部 `null` 转为 `Number(null) === 0`，因此真实一行会显示“共 0 行”。这不是截图等待问题。

## 分类、参考与范围

OneID：不涉及；只读 share 使用既有 opaque token，不解析、建立或关联客户身份。

Persistence / External Effects：不涉及；不改 Product/Order 查询、分享凭证、数据库、任务、Provider 或写入。

GitHub 仓内参考：[PR #201](https://github.com/qianlan33333-png/AI-CRM-v3/pull/201) 的 V3 `member_grid_host.js` 已作为冻结 dd8 资源前的受控 Host seam，处理 V3-owned response adaptation 而不改 donor；本修复复用该 seam。冻结 `member_grid_donor/*` 与 `SHA256SUMS` 不改。

范围仅在 V3 Host 中：公开页 donor summary 因 `null` 被错误转为“共 0 行”时，改为“当前显示 N 行”，其中 N 是此刻 `#spGridBody` 中实际渲染的数据行。分组折叠会使 N 为 0，展开和末页追加会重新计算；该文案不承诺全量、已加载总数或另建分页状态。明确总数、错误和撤销分享状态保持原有行为。

## 验收

1. 真实公开 share 的一条可见行显示“当前显示 1 行”，不显示“共 0 行”。
2. 分组折叠后显示“当前显示 0 行”，展开后回到“当前显示 1 行”；不把被折叠的数据称为已加载或全量。
3. 真实 cursor 末页追加两条成员后，三条可见行显示“当前显示 3 行”；这只验证当前渲染结果，不承诺全量。
4. 公开 token 仍只在 fragment/header 中使用，DOM 不泄漏；无效 token 不返回数据。
5. donor 哈希保持不变，Host 仍在 donor scripts 之前加载。
6. 现有 member-grid browser journey、Product HTTP tests 和最终 PostgreSQL + Chromium 路由验收通过。
