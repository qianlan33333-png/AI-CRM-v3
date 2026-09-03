# PR08 运营周期完整闭环复核

## 结论

PR08 现在在 PR10 的唯一 admin shell 中装配冻结 donor 前端链，并以最小 v3 只读绑定
提供 operationcycle-owned 的安全投影。它仍不是 Provider/recipient 写闭环：donor 主
action 保持原样 blocked，页面不会启动 execution runtime 或发起写请求。

冻结供体的活动前端严格只有两个 fragment：`cycles.html` 和
`cyclesDetail.html`。两者在 donor `6bfbe5816bb89913c70adaca87d6a486260e016e`
上逐字节一致；没有专属 TS、CSS、图标或静态资源。这不是遗漏：列表的循环、插值、
`t.act`/`t.viewDetail` hook 和详情 query 路由都依赖 shared `main.ts → legacy.ts →
AdminController`。PR01 已将这条完整链作为冻结 donor runtime 引入 v3；PR08 必须**复用
同一条已冻结原链**，不能再复制一套，也不能为 cycles 自建替代 loader。

因此，遵循“前端 100% 原样优先、禁止额外发挥”：

- 可以冻结、校验、保存两个原始 fragment；不能改其中任何字节。
- `main.ts`、`legacy.ts`、`controller.ts`、`types.ts`、`client.ts`、`mockData.ts` 的
  已冻结 PR01 副本可被复用，但 PR08 不得修改字节、另复制、替换或剪裁它们；nav、
  registry、`build.mjs` 和任何第二 v2 shell 仍不得装配。
- 不得为了让页面动起来新增 cycles TS/CSS、浏览器 mock、sessionStorage fallback、
  HTML 插值/循环 loader 或自定义 action UI。
- PR10 将原 fragment 与已冻结 runtime 挂入唯一 v3 单侧栏。v3 host binding 在加载
  donor `main.ts` 前仅覆盖 `AdminApi.loadDb` 的 `cycles/cyclesDetail` 读取，映射
  operationcycle API 的 typed snapshot projection；不修改 donor runtime，也不回退 Mock。

精确阻断是：**无法在不发明新的前端请求或交互的情况下完成浏览器写闭环**。不是
“不能挂载”或“不能复用 runtime”。

## 已验证的前端事实

| 项目 | 事实 | 交付含义 |
| --- | --- | --- |
| 页面 | `admin/cycles.html`、`admin/cyclesDetail.html?id=<positive>` | exact allowlist 仅两项 |
| 资源 | 无 cycle 专属 TS/CSS/assets；PR01 已冻结 shared runtime | 必须复用原链，不可自建专属页面脚本或样式表 |
| 列表渲染 | `sc-for`、Mustache 插值、`cycles.rows` | 原始 fragment 单独作为静态 HTML 不可读真实数据 |
| 列表 action | donor controller 明确 `blocked('...DTO 不等价')` | 不存在可迁移的浏览器 start 行为；不得把按钮改成新 API |
| 详情跳转 | donor controller 将 `runId` 数字映射 `?id=` | v3 仅把数字视为当前稳定排序的 display ordinal，重新读取后解析本地 run key；不作为业务身份或写入参数 |
| HTTP mode | donor `readAdminRows/readAdminPage` 没有 cycles 分支 | v3 host binding 先安装只读 `loadDb` 再启动原链；读取失败或缺少安全 dossier 会 fail closed，不回退 mock |
| mock mode | `SEED_DB` + sessionStorage 仅演示 | 禁止作为 v3 数据源或 fallback |

`scripts/check-pr08-frontend-donor-manifest.sh` 已实际通过：两个 source blob、目标
hash 和 `cmp` 均匹配；target set 恰为两个 HTML，且不含 v2 document/sidebar shell。

## PR10 单侧栏的允许方式与当前阻断

PR10 应复用 PR01 已冻结的 `main.ts → legacy.ts → AdminController`，由 v3 单壳和
后端 allowlist 限制开放面；不另造 renderer。允许的装配为：

```text
v3 session + CSRF gate
        -> /admin/operation-cycles (v3 admin_base，唯一 .admin-sidebar)
        -> PR01 frozen main.ts -> legacy.ts -> AdminController
        -> byte-exact cycles.html / cyclesDetail.html
        -> operationcycle HTTP DTO adapter
```

运行时不得引入第二个 `.side`、`.shell`、v2 nav、mock DB、sessionStorage、旧 URL 或
自行设计的交互。挂载验收须断言页面只有一个
`.admin-sidebar`，没有 donor `.side`/`.shell`，且 fragment 文件仍通过 SHA/cmp。

详情 canonical route 应为
`/admin/operation-cycles/{strategy_key}/runs/{run_key}`，由后端持久化的本地 key
解析，不能接受 donor 数字 `id` 作为业务身份。现有原链的 `t.viewDetail` 是
`?id=<runId>`；在兼容 adapter 尚不能以既有原链把它解析到 canonical identity 前，
只允许明确 blocked，不能添加新的前端跳转。

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

- 页面没有真实 HTTP mode 的 cycles request；接入 v3 读 DTO 前，原链只能得到空
  rows/无可靠 detail DTO，不能声称浏览器读 Journey 已由这些 API 覆盖。
- donor 列表 action 在原 `AdminController` 中是明确 blocked，PR08 前端不能把它接到
  start endpoint；否则就是发明 donor 不存在的浏览器请求。
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
已冻结原链本身不能产生 cycles HTTP read；在用户不允许任何前端新增的约束下，不能承诺
“列表真实读 → 详情刷新”的 browser Journey。不存在的 action 交互不纳入前端 Journey，
也不补造；若未来要改变此结论，需用户单独授权一份新的冻结 donor 前端合同，而不是由
Terra/Luna 自行添加请求。

## 本次复核结论

PR08 audit `31d3d78d92c696a350f72e2b4f0758c4cffef8cf` 对冻结输入的描述准确；本复核
没有发现可直接搬运但未登记的 cycles 前端文件。最大的风险是忽略 PR01 已冻结原链而
另造 runtime，或将原链能渲染空态误报为 HTTP 写闭环。故 PR08 应标记为
**frontend-write-contract blocker**：可复用原链做原样渲染，但其 HTTP mode 既没有
cycles read request，写 action 又明确 blocked。必须等待用户对新的冻结前端合同作出
范围决策，不能由 Terra/Luna 自行添加 request 后继续搬运。
