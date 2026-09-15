# 公开会员表未知总数呈现 PRD

日期：2026-09-16  
状态：已授权实施；修复公开只读会员表的错误行数文案。

公开 `/shared/service-period-member-grid` 的既有 Product HTTP query 有行和游标，却刻意不承诺全量 `total`。冻结 dd8 renderer 把其内部 `null` 转为 `Number(null) === 0`，因此真实一行会显示“共 0 行”。这不是截图等待问题。

## 分类、参考与范围

OneID：不涉及；只读 share 使用既有 opaque token，不解析、建立或关联客户身份。

Persistence / External Effects：不涉及；不改 Product/Order 查询、分享凭证、数据库、任务、Provider 或写入。

GitHub 仓内参考：PR #201 的 V3 `member_grid_host.js` 已作为冻结 dd8 资源前的受控 Host seam，处理 V3-owned response adaptation 而不改 donor；本修复复用该 seam。冻结 `member_grid_donor/*` 与 `SHA256SUMS` 不改。

范围仅在 V3 Host 中：公开页已有数据行且 donor summary 显示不可能的“共 0 行”时，改为准确的“已加载 N 行”。未知总数绝不伪造全量计数；零行、明确总数、错误和撤销分享状态保持原有行为。

## 验收

1. 真实公开 share 的一条行显示“已加载 1 行”，不显示“共 0 行”。
2. 公开 token 仍只在 fragment/header 中使用，DOM 不泄漏；无效 token 不返回数据。
3. donor 哈希保持不变，Host 仍在 donor scripts 之前加载。
4. 现有 member-grid browser journey、Product HTTP tests 和最终 PostgreSQL + Chromium 路由验收通过。
