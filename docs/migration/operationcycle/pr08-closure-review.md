# PR08 运营周期完整闭环复核

## 结论

PR08 当前只能作为 **donor 证据归档和后端预备契约**，不能进入实现或部署队列，更
不能把 `/admin/operation-cycles` 的 PR10 占位壳替换成半成品页面。

冻结供体的活动前端严格只有两个 fragment：`cycles.html` 和
`cyclesDetail.html`。两者在 donor `6bfbe5816bb89913c70adaca87d6a486260e016e`
上逐字节一致；没有专属 TS、CSS、图标或静态资源。这不是遗漏：列表的循环、插值、
`t.act`/`t.viewDetail` hook 和详情 query 路由都依赖混合了客户、人群、群运营和 mock
逻辑的 shared `AdminController`。该 runtime 不能复制，也不能为 PR08 自建替代 loader。

因此，遵循“前端 100% 原样优先、禁止额外发挥”：

- 可以冻结、校验、保存两个原始 fragment；不能改其中任何字节。
- 不得复制 `main.ts`、`legacy.ts`、`controller.ts`、`types.ts`、`client.ts`、
  `mockData.ts`、`nav.json`、`registry.json`、`build.mjs` 或任何 v2 shell。
- 不得为了让页面动起来新增 cycles TS/CSS、浏览器 mock、sessionStorage fallback、
  HTML 插值/循环 loader 或自定义 action UI。
- 在不存在一个已批准、通用、且不改变 fragment 行为的 v3 fragment-runtime 前，PR10
  **不能挂载**这两个页面；继续显示 v3 已认证单侧栏的明确“未提供”状态才是正确
  fail-closed 行为，而不是交付失败。

这个阻断来自冻结前端本身，不是后端 schema 可以补平的问题。

## 已验证的前端事实

| 项目 | 事实 | 交付含义 |
| --- | --- | --- |
| 页面 | `admin/cycles.html`、`admin/cyclesDetail.html?id=<positive>` | exact allowlist 仅两项 |
| 资源 | 无 cycle 专属 TS/CSS/assets | 不可自行补一个页面脚本或样式表 |
| 列表渲染 | `sc-for`、Mustache 插值、`cycles.rows` | 原始 fragment 单独作为静态 HTML 不可读真实数据 |
| 列表 action | donor controller 明确 `blocked('...DTO 不等价')` | 不存在可迁移的浏览器 start 行为；不得把按钮改成新 API |
| 详情跳转 | donor controller 将 `runId` 数字映射 `?id=` | 不可把数字值当 v3 strategy/run 身份；无原 controller 时也无 hook |
| HTTP mode | `readAdminRows/readAdminPage` 没有 cycles 分支 | donor HTTP mode 不能真实读 cycles、详情或执行 action |
| mock mode | `SEED_DB` + sessionStorage 仅演示 | 禁止作为 v3 数据源或 fallback |

`scripts/check-pr08-frontend-donor-manifest.sh` 已实际通过：两个 source blob、目标
hash 和 `cmp` 均匹配；target set 恰为两个 HTML，且不含 v2 document/sidebar shell。

## PR10 单侧栏的允许方式与当前阻断

未来仅当已有批准的 v3 通用 fragment-runtime 可以完整解释 donor 已有的
`sc-for`/插值/`onClick` 行为时，PR10 可采用下列装配：

```text
v3 session + CSRF gate
        -> /admin/operation-cycles (v3 admin_base，唯一 .admin-sidebar)
        -> approved fragment runtime
        -> byte-exact cycles.html / cyclesDetail.html
        -> operationcycle HTTP DTO adapter
```

该 runtime 必须在 PR08 之外先被评审；它不能引入第二个 `.side`、`.shell`、v2 nav、
mock DB、sessionStorage、旧 URL 或自行设计的交互。挂载验收须断言页面只有一个
`.admin-sidebar`，没有 donor `.side`/`.shell`，且 fragment 文件仍通过 SHA/cmp。

详情 canonical route 应为
`/admin/operation-cycles/{strategy_key}/runs/{run_key}`，由后端持久化的本地 key
解析，不能接受 donor 数字 `id` 作为业务身份。若无法在不改前端的前提下把现有
`go.cycles`/`t.viewDetail` 映射到这一语义，页面继续不可用；不能添加新的前端跳转。

## 后端闭环设计（仅作为 Terra 实施清单）

OneID：**不涉及**。策略、run、runner、action request、proposal 都是本地运营事实；
禁止 customer、external user、audience/segment/campaign/recipient、订单、会员、权益
或身份绑定。模板出现的人群、发送和交付文案只能作为冻结展示文字，不能读取或写入
这些域的数据。

Persistence：**operationcycle-owned PostgreSQL UoW**。需要的 additive schema：

| Owner 表族 | 必需事实 |
| --- | --- |
| `operation_cycle_strategies`、`operation_cycle_strategy_versions` | 本地 strategy key、版本、draft/active/paused/archived、定义/snapshot digest |
| `operation_cycle_runs`、`operation_cycle_run_snapshots` | run key、strategy key、revision、received local snapshot、不可变证据 |
| `operation_cycle_runners`、`operation_cycle_runner_heartbeats` | runner compatibility、binding、lease/heartbeat；仅本地事实 |
| `operation_cycle_action_requests`、`operation_cycle_action_events` | queued → claimed → thread_bound → turn_started → completed/failed，CAS/fence/原键重放 |
| `operation_cycle_proposals`、`operation_cycle_proposal_decisions` | pending/accepted/rejected，decision actor/time 审计 |
| `operation_cycle_receipts`、`operation_cycle_audit_events`、`operation_cycle_outbox` | idempotency、payload drift、审计与 outbox，同一事务提交 |

所有业务状态、receipt、审计和 outbox 必须同一 PostgreSQL transaction；Store 只能写
上述 owner 表。Provider 网络调用不持事务。本轮没有 Provider 写；将来若新授权企微
或其他写，必须另立能力，经 `outbound` 与 External Effects，且区分 accepted/queued/
attempted/executed/outcome_unknown/reconciled。

冻结 donor 后端路径可作为 HTTP 合同证据：管理员侧 strategy/run/action/proposal 读写，
以及 service-authenticated claim/event/report/heartbeat/context。Terra 可为等价本地
事实提供 v3 session、CSRF、角色、OpenAPI 和 DTO adapter；但是：

- 页面没有真实 HTTP mode 的 cycles request，不能声称浏览器动作已由这些 API 覆盖。
- donor 列表 action 是明确 blocked，PR08 前端不能把它接到 start endpoint。
- 未有 DOM/replay 的模板字段必须是持久化等价事实；缺失时 fail closed，不填 mock
  数字，不把 queued 冒充执行或发送成功。
- 对 `outcome_unknown` 只允许原键对账/审计，禁止换 idempotency key 盲重试。

## Included / excluded 与 Journey 门槛

可纳入后端闭环的范围是：本地 strategy/version/run/runner/action/proposal 生命周期、
本地 report/heartbeat、状态/版本/审计/幂等/Outbox、管理员和 service API 的权限
边界。前提是每项真实存储、可刷新读取、可审计、可并发验证。

明确排除：问卷、雷达、客户/OneID、客户标签、受众包、Campaign、订单支付权益会员、
客户企微打标、recipient 执行、实际群发/发送回执、V1 static history import，以及
任何 mock/sessionStorage/v2 runtime。

后端 Journey 最低门槛：管理员登录 → 创建/版本/状态操作 → 刷新仍存在 → 审计可查；
action 需覆盖 idempotent start、payload drift、CAS、重启/lease、terminal
`outcome_unknown`；proposal 需覆盖 decision audit。只有上述后端闭环与一个**已存在且
批准**的原样 fragment runtime 同时满足，才能添加前端 Journey：登录 → 列表真实读 →
donor 已有详情 hook → 真实详情刷新。不存在的 action 交互不纳入前端 Journey，也不补造。

## 本次复核结论

PR08 audit `31d3d78d92c696a350f72e2b4f0758c4cffef8cf` 对冻结输入的描述准确；本复核
没有发现可直接搬运但未登记的 cycles 前端文件。最大的风险是将两个 fragment 误认为
完整可运行页面，或为填补其 shared runtime 缺口而违反“100% 原样”约束。故应把 PR08
标记为 **frontend-runtime blocker**，等待一个另行授权的通用 v3 renderer 决策，而不是
直接开启 Terra/Luna 功能搬运。
