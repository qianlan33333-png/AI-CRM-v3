# Global UI 执行状态

更新时间：2026-09-15

本文是当前事实台账，不把目录存在、接口骨架、CI 通过、合入、部署开关或截图单独当作业务完成。状态按“分支／CI → 合入 main → 发布 → 认证浏览器读回 → 真实业务/provider 回执”分层记录；未满足的层级继续保留。

## 当前主线与本次治理边界

| 项目 | 当前事实 | 结论 |
| --- | --- | --- |
| 当前 main | `84c34e5ad784d3f4cf20082b6a83d39919bff7e5` | 已核实；该提交为 PR #317 squash merge commit。 |
| PR #317 | review head `09cf5d7ff6889834b6e0fd79cea88cd39d55836e`，于 `2026-09-15T03:29:19Z` squash 合入 | main 与 review head 的 tree 均为 `6838f97709d2e9845094a5cd2f595571be70cac7`；#317 的共享选择／内容／状态能力已在 main。 |
| #317 CI | run `34924244166` | plan、preflight、backend、frontend、browser、archive-sdk、check、quality-report 全部 PASS；deploy 为 SKIPPED。 |
| 当前 PR | #297，分支 `codex/global-ui-governance-20260915`，独立工作树 `/private/tmp/aicrm-global-ui-governance-20260915` | 本次只改治理文档与规则入口；原目录只读保留，不改业务源码、不合并、不部署。 |
| 部署开关 | `gh variable list` 未找到 `AICRM_ENABLE_ACTIONS_DEPLOY`；仓库变量当前只见 SSH 连接变量 | 没有部署证据。 |
| 线上读回 | 生产 IAB 只读访问超时，未得到认证页面、API 或 provider 回执 | 不能称为已上线或生产验收。 |

PR #317 的 squash tree 同时承载了已关闭的 #287、#296、#300、#303、#305、#307、#310、#311、#315；这些 PR 的能力随 #317 进入 main，但各自原分支保留，不能再按“未合入”描述。main 在此之前还包含已合入的 #318、#319、#321、#323（#323 的 exact-ID 修复 merge commit 为 `22e24ddcf646252985257fe55370a8f5fe3890f9`）。除明确列出的 main 事实外，其余本任务 PR 均未合入、未部署。

## 仍在推进的能力

| 能力／PR | 当前 head 与 GitHub 状态 | 已核实证据 | 未完成或边界 |
| --- | --- | --- | --- |
| 商品售卖信息分销配置／[#291](https://github.com/qianlan33333-png/AI-CRM-v3/pull/291) | `bf5409dc4ab95c813f040a33752dc9e05bbba12d`；OPEN，非 Draft，`BEHIND` | run `34898498514` attempt 2 的必跑 lane PASS；实现要求为售卖信息唯一开关、比例和退款复核等待天数。 | 尚未合入或发布；商品页完整认证读回待 owner 处理。编辑页不得出现分销员申请入口、链接、复制按钮或二维码。 |
| 经营首页只读 API／[#293](https://github.com/qianlan33333-png/AI-CRM-v3/pull/293) | `62bb9989cd0b0a4d9c1b4d26d344e25c37381c42`；OPEN，非 Draft，`DIRTY` | run `34920656797` 必跑 lane PASS；canonical payer RR 稳定读去重修复已在分支。 | 当前与 main 冲突，待 Terra 处理；无合入、部署或生产读回。 |
| 经营首页与导航／[#299](https://github.com/qianlan33333-png/AI-CRM-v3/pull/299) | `7e07955b9c5c89b8094b5e4c7c12c3364ecb831f`；OPEN，Draft，基于 #293 head `62bb9989…` | 本地 full consumer ASCII PASS。 | GitHub 尚无该 head 的 CI；未合入、未部署。页面截图或本地结果不能替代生产认证读回。 |
| 交易订单分销事实／[#301](https://github.com/qianlan33333-png/AI-CRM-v3/pull/301) | `961173dae8661410fc77353cfad75c39be14d5cc`；OPEN，非 Draft，`DIRTY` | run `34914087652` 的 plan、preflight、backend、frontend、browser、archive-sdk、check、quality-report PASS，deploy SKIPPED。 | 分支仍有 main 冲突；订单快照、复核期、预计／实际结算与异常证据还需最终 owner readback；CI 通过不等于发布。 |
| 分销后台／[#309](https://github.com/qianlan33333-png/AI-CRM-v3/pull/309) | `6e794f84042da6ae67708f20e8501fccbc0df43e`；OPEN，非 Draft，`DIRTY` | run `34907120228` 必跑 lane PASS，deploy SKIPPED。 | 未合入、未部署；需逐页认证业务数据与订单证据验收。 |
| 客户 SSR 档案／[#316](https://github.com/qianlan33333-png/AI-CRM-v3/pull/316) | `7a91c6ba4849dceb6845a6666144c5e4c9b3d73b`；OPEN，非 Draft，`DIRTY` | run `34914852224` 必跑 lane PASS，deploy SKIPPED。 | 客户归属、OneID 摘要、分页与权限读回仍未形成发布证据。 |
| 客户分销确认／[#312](https://github.com/qianlan33333-png/AI-CRM-v3/pull/312) | `26782141327a3e374f654b6496cdc510a0fd92c4`；OPEN，非 Draft，`BEHIND` | run `34909109690` 必跑 lane PASS，deploy SKIPPED。 | 未合入、未部署；客户结算确认和分销事实仍需最终 main 同步与认证读回。 |
| 自动化固定话术／[#322](https://github.com/qianlan33333-png/AI-CRM-v3/pull/322) | `d6dd434c73155e6216d3196150350a0e22844ba1`；OPEN，Draft，`DIRTY` | 本地 full consumer + 真实 PostgreSQL Chromium 的授权保存+GET readback PASS；Prompt 与固定话术保持分离。 | run `34922162119` 的 browser、check FAIL，browser 原因是 `automation_fixed_content_chromium_postgres_integration_test.go:39` 的 manifest 缺少 `automationContentHost`；未合入、未部署，最终 CI、发布和线上读回未完成，不能以本地证据覆盖 CI 失败。 |
| 公共商品与支付／[#325](https://github.com/qianlan33333-png/AI-CRM-v3/pull/325) | `327b89dd897f85c9a5d853766acbf19bef1cc02a`；OPEN，Draft，`DIRTY` | 第一轮只证明 auth gate／mount；publicCommerceJourney 本身 PASS（8.13s）。run `34924863876` 的 browser、backend 最终 FAIL；browser 另有 composition 硬编码 `web/dist` 导致 manifest `ENOENT`。 | 真实有效会话访问未上架周期商品会触发 nested UoW `503`（Payment session txctx 传入 Order 自有 UoW）；Terra 正修复只读组合并补权益／失效会话 PostgreSQL 回归，尚未验收。旧 authgate 通过不构成商品实质页面或支付状态验收。 |
| 首页已支付指标下钻／[#326](https://github.com/qianlan33333-png/AI-CRM-v3/pull/326) | `c1520e3cdc4e6491cd86676e8848d5e443dfa439`；OPEN，Draft，基于 #299 | 本地 PG/Chrome v8 日志 `/private/tmp/aicrm-paid-drilldown-chromium-20260915-v8.log`；root 已复核 1280 支付明细抽屉和 1440 订单内容截图。 | synthetic fixture 未启用 WeChatPay 受控组合分支，`Order SetDistributionReader` 未安装，所以分销事实不可读；退款完成 2 但可退 12 是 fixture 直接插入 refund 未同步 `orders.refunded_minor`。这两项是待 owner 联动 fixture 的问题，不能当线上 bug 或订单分销全链验收；其它指标下钻仍缺。 |
| 素材消费者 | 最新工作树提交 `2fa1fa27` 已同步 main `84c34e5…`，尚未形成本 PR | Terra 正在补 Product 真实多选、Radar 单项素材的回显和 save/GET；image/PDF 同 ID 类型切换 bug 处理中。 | Radar owner 只允许一个 image 或 PDF `media_item_id`，不能按共享 picker 的多选能力验收；Product preopen 仍有裸 fetch、无 deadline／single-flight／epoch 的风险，不能冻结通过；不把工作树状态写成完成。 |

## 共享组件与状态示例

PR #317 已把 V3-owned `SelectionSession`、selection dialog、tag/staff/group/material adapter、content composer/presentation、standard host 与 component-state demo 合入 main。共享组件测试和集成 CI 只证明相应合同，不替代实际调用页面的授权数据、错误态、保存读回或 provider 回执。

| 组件 | main 中的真实入口与调用 | 当前状态示例／证据 | 仍缺的真实覆盖 |
| --- | --- | --- | --- |
| Group | `web/v3/shared/ui/groupPickerAdapter.ts`；`groupOpsHostAdapter.ts` 与 `componentStatesHost.ts` | GroupOps 的 owner scope、分页、`chat_reference`、失效回显、403 保留、保存锁和 readback 由 #300 分支 CI `34892980966` 覆盖；`/admin/component-states` 以本地 loader 展示 loading/empty/error/forbidden/readonly/invalid。 | 非 GroupOps 页面不能套用该 scope；生产发布和其它调用页仍待验收。 |
| Material | `web/v3/shared/ui/materialPickerAdapter.ts`；Product、Radar、GroupOps 与 component-state demo | #296 的 adapter 状态测试、Radar renderer callback 和本地 Group/Material 状态示例已随 #317 入 main；失败保留 draft、缩略图 fallback、重复确认锁有合同证据。 | Radar 业务只允许一个 image 或 PDF `media_item_id`，仍需单选／替换／移除／重开回显和 save/GET；不能因共享 picker 支持多选而扩大 Radar 合同。Product 等其它 caller 是否多选由各自 owner 另行验收。 |
| Tag | `web/v3/shared/ui/tagPickerAdapter.ts`、`standardComponentsHost.ts`；当前真实调用为 Channel、Customer、Product Host | #305 的真实 host 接入能力随 #317 入 main；共享 adapter 合同有单/多选、分组、失效、403、刷新与失败保留测试。 | `/admin/component-states` 尚无 Tag 的独立状态示例；问卷及其它调用页不能因标准 Host 存在而标完成。选择确认不自动给客户打标。 |
| Staff | `web/v3/shared/ui/staffPickerAdapter.ts`、`standardComponentsHost.ts`；Channel 与 GroupOps 使用 V3 adapter，冻结员工 picker 仍由兼容 Host 装配 | #311 的 scope 约束与 adapter 合同随 #317 入 main；Channel 有真实授权 scope、刷新和保存前草稿测试。 | `/admin/component-states` 尚无 Staff 状态示例；非群运营目录不能套用 GroupOps 范围，Customer/owner migration 等调用仍需真实页面验收。 |
| Composer／内容只读 | `web/v3/shared/ui/contentComposer.ts`、`contentPresentation.ts`；GroupOps 与 `excelBatches.ts` 调用 | #307/#310 的编辑、素材排序、预览/只读合同随 #317 入 main；预览不触发发送。 | `/admin/component-states` 尚无 Composer 状态示例；#322 固定话术仍有 browser/check FAIL，Prompt 分离和真实保存回读待完成。 |

现有状态示例页 `/admin/component-states` 只覆盖 Group、Material 及表单／IME 本地示例，不调用 Provider、不保存业务数据。Tag、Staff、Composer 的状态示例仍欠；其余后台、企微 sidebar、H5 和公开问卷页保持独立壳、授权范围和逐页验收，不能由状态 demo 或单个共享组件测试升级。

## 证据级别与页面矩阵

`skills/aicrm-v3-frontend-consistency/references/component-map.md` 的 99 条矩阵条目混合 canonical route、alias、reserved placeholder、login/logout 和构建 artifact，不能称为 99 个 canonical 页面。每条按实证维护最高级别：

| 级别 | 允许的证据 | 不代表 |
| --- | --- | --- |
| C0 | 源码路由、handler／adapter、`Render*`／mount 与 manifest assets 静态核对 | 页面可用或业务成功 |
| C1 | 共享组件合同测试、single-flight／状态／焦点／IME 等行为测试 | 实际调用页已接入或有业务数据 |
| C2 | 实际挂载壳、视口布局与失败／空态等视觉或交互证据 | 认证业务 readback 或发布 |
| C3 | 认证业务数据、关键交互、保存后服务端 readback，必要时有 provider 事实 | 生产部署；仍需独立发布和线上回读 |

当前仅在明确范围内记录 C1–C3；未列为明确证据的矩阵行保持 C0。共享组件状态示例本身不把任何业务页升级为 C3。详细 route、Host、assets、视口、调用边界和待办以 component-map 为准。

截图仅作对应视口参考；CI、合入、部署、认证浏览器读回和 provider receipt 分开记录，不能互相替代。
