# 前端真实组件索引（本次分销能力补充）

以当前仓库路径为准。索引用于选择现有入口，不授权跨领域读取或写入；动工前除路径外，还要追踪 canonical 路由、handler／adapter、`Render*` 或挂载、assets 和页面调用。未挂载实现与历史快照不作为当前标准。

| 场景 | 入口 | 调用／装配 | 边界 |
| --- | --- | --- | --- |
| 管理端单壳 | `internal/webshell/templates/admin_base.html` | `internal/webshell/renderer.go` 的 `RenderDistribution` | 仅一个 `.admin-sidebar` 和 `.admin-topbar`；分销领域只提供经 manifest 验证的正文 assets。 |
| 问卷管理与编辑器 | `web/v3/surveyAdapter.ts`、`web/src/admin/sections/questionnaireEditor.ts` | `internal/survey/ui.go` 解析 manifest，`RenderSurvey` 在冻结 admin bundle 前装配列表 bridge；编辑器由 `questionnaireEditor` 入口挂载 | 一级列表复用冻结 table、检索和操作；bridge 仅映射问卷名称。编辑页保留标题用于答题/预览，状态以服务端 readback 为准。 |
| 通用详情抽屉 | `web/v3/shared/ui/detailDrawer.ts`、`web/v3/shared/ui/detailDrawer.css` | `distributionAdmin.ts` 导入行为，`sharedDetailDrawerStyles` 由 `admin_base` 挂载 | 组件只负责焦点回收、Escape/关闭和视觉容器；调用方必须读取自己的受权服务端事实，不能由抽屉伪造状态。 |
| 商品编辑分销配置与申请入口 | `web/v3/productAdapter.ts`、`web/v3/productDistribution.css` | Product UI 的 `ProductCSS` 经 `admin_base` 装配 | 沿用商品编辑表单 token 和公开申请 URL／二维码，不新增壳、配色或身份／资金模型。 |
| 分销管理详情 | `web/v3/distributionAdmin.ts`、`web/v3/distribution.css` | `RenderDistribution` 的 `#distribution-admin-root` 和 Distribution manifest assets | 只使用现有后台 table、tab、button token；公共分销中心样式必须限定在 `body[data-ui-surface="distribution"]`，不能重置后台全局样式。 |

## 全局统一 UI 增补清单（2026-09-15）

本节登记本轮实际 Host、受权读取和未完成合同。标记为 PR 的能力尚未合入 main；不能因为共享模块或组件测试存在而把未列的页面标为覆盖。商品编辑分销行由商品专项 PR 单独维护，避免与该专项并行修改。

| 场景 | 入口 | 调用／装配 | 已核实状态与边界 |
| --- | --- | --- | --- |
| 管理端全局反馈 | `web/v3/surfaceFeedbackHost.ts` | 构建脚本向生成的 admin、H5、sidebar 与分享文档注入反馈 Host | 只映射加载／资源失败表现；不把业务失败伪装成成功，也不改变领域命令。 |
| 已提交文本搜索，第一批 | `web/v3/shared/ui/committedTextSearch.ts` | 渠道：`channelCenterAdapter.ts`；标准选择器：`standardComponentsHost.ts`；其他生成页面：`surfaceFeedbackHost.ts` | **PR #287，未合入。** 只精确注册渠道、优惠券、问卷、标签、计划、素材库与标准群聊／标签／客服／素材选择器控件；不全局拦截表单。渠道真实 Chromium 输入法 Journey 已通过，其他匹配页面仍须逐页业务验收。 |
| 标签选择器 | 冻结 `wecom_tag_picker.js`；V3 `standardComponentsHost.ts` | Products、Channels、Radar、Survey 由各自 `StandardHostJS` 装配；Customers 在 release HTML 注入 stable Standard Host | 现有调用使用页面提供的标签目录／回调；不由选择器给客户打标。完整 V3 session 合同未接入。 |
| 客服／员工选择器 | 冻结 `operation_member_picker_dd8d60d.js`；V3 `standardComponentsHost.ts` | 标准 Host 加载并处理已注册的刷新动作 | 当前刷新沿用 group-ops scope 的既有调用，不可作为其他客服页面的通用范围；真实非群运营调用与 `SelectionSession` 迁移未完成。 |
| 真实群聊选择器 | 冻结 `group_chat_picker.js`；V3 `selectionSession.ts`／`groupPickerAdapter.ts` | 冻结 Standard Host 仍在 Products、Channels、Radar、Survey 等页面装配；[PR #300](https://github.com/qianlan33333-png/AI-CRM-v3/pull/300) 的 GroupOps 使用 V3 session | main 继续冻结既有组件；PR #300 已在真实 GroupOps 接入 Owner scope 分页、失效回显、`chat_reference` 和保存读回，CI `34892980966` 的 plan/preflight/frontend/browser/backend/archive/check 全通过；仍未合入或部署。其他 Standard Host 尚未迁移，不能套用 GroupOps 证据。群邀请素材命令不在此项。 |
| 素材选择器：Radar | `web/v3/shared/ui/selectionSession.ts`、`materialPickerAdapter.ts`、`radarAdapter.ts` | **PR #296，未合入。** `RenderRadar` 的 `RadarAssets.StandardHostJS` 先加载标准全局，Radar 明确注入图片／附件目录 loader 后接管可视 dialog | 实际冻结 Radar renderer callback Journey 验证单项添加；调用没有 `selectedRecords`／`onCommit`，多选移除与重新打开回显尚不是 Radar 页面验收。组件不读取 `AdminApi`、不扩大目录 scope。 |
| 素材选择器：产品及其他调用 | `web/v3/productAdapter.ts`、冻结素材 picker | `RenderProducts` 的 `ProductAssets.StandardHostJS` 与 Product Host | 当前产品仍为单项 legacy callback；不得写成已迁移 V3 material session。后续迁移须由调用方提供受权 `loadPage`、实际 `selectedRecords` 与 `onCommit`。 |
| 话术／内容编辑器 | 冻结 `send_content_composer.js` 与 V3 `standardComponentsHost.ts` | 同标准 Host 的页面加载；各领域 adapter 维持业务命令 | 尚未完成统一 V3 编辑／预览／只读合同及真实页面验收；预览不能触发发送。 |
| 经营首页与导航 | overview 的 `overviewAdmin.ts`、`navigationHost.ts`、`admin-navigation.v3.json` | API 由 [PR #293](https://github.com/qianlan33333-png/AI-CRM-v3/pull/293) `de567042885a364adc36037b20fa733ea8a02e82` 单独记录；正文与导航由 [PR #299](https://github.com/qianlan33333-png/AI-CRM-v3/pull/299) `faa906b600517ca983af256900c8d91f9396f14b` 装配 | PR #299 CI 已通过；尚无合入、部署或生产读回，不能写成已发布。 |
| 订单分销事实展示 | 交易 Order Host 与 distribution Stable Read Port | 当前 PR #301 已装配，frontend CI 尚未通过 | 详情只读取订单级快照、复核期、预计／实际结算及异常证据；不得读取商品当前比例或跨领域写入。 |

未列为「已核实」的页面不因路径相似而复用上表读取范围或命令。每次迁移更新本表时记录 canonical 路由、实际 Host／asset、数据来源和授权范围、选择回调、状态验收及尚未完成合同项。

## 路由／页面条目逐项验收矩阵（2026-09-15）

本矩阵以历史 clean main `3eda04cbd56d7bfdf44ba2a15d573ee29703926a` 的实际路由装配为初始盘点基线；当前 main `9de7c37312f08e531ecaf1956b874a9e5c745511` 仅新增 PR #302 的 Excel 分页和 PR #288 的 Channel Center 稳定 ID archive，不重写本轮路由盘点。未合入的选择器改动单独绑定最终冻结头素材 `096779ce910c58ce99b8ed9ee1282e774fde3308`、群聊 `b5cc35814b6b2c29899ae727a9c9c646091faa9c`。条目总数以表中连续编号自动核算，涵盖 canonical route、alias、reserved placeholder、登录／退出和构建 artifact，不能描述为相同数量的 canonical 页面。`C0` 只表示源码路由、Host 和 assets 静态核对，不能当作页面通过；`C1` 是共享组件合同测试，`C2` 是壳与视口证据，`C3` 才是认证业务数据和保存后读回。视口要求按页面类型执行：后台桌面 `1280/1440`、企微 sidebar `360/420`、公开或 H5 `375/390/430`。除“已测证据”明确列出的子集外，表中的“状态”统一表示“未执行/待验收”的逐项检查集合。

Host／assets 缩写：`WB`=`webshell.RenderAdmin` + `admin_base`；`CH`=`RenderChannels` + `channelCenterHost/standardComponentsHost`；`SUR`=`RenderSurvey` + `surveyHost/questionnaireEditor/standardComponentsHost`；`RAD`=`RenderRadar` + `radarHost/standardComponentsHost`；`GRP`=`RenderGroupOps` + `groupOpsHost/operationPicker/groupPicker/materialPicker/composer/readonly`；`AI`=`RenderAIAssistant` + `aiAssistantHost` 及 group/material/composer/readonly；`ORD`=`RenderOrders` + `orderHost`；`PROD`=`RenderProducts` + `productHost/standardComponentsHost`；`COUP`=`RenderCoupons` + `couponHost`；`MED`=`RenderMedia` + `materialSaveHost/imageLibraryFilterHost`；`AUT`=`RenderAutomation`；`OP`=`RenderOperationCycles` + `operationCyclesHost`；`CFG-V3`=`RenderRuntimeConfig`；`CFG-D`=`RenderConfig`；`DIST`=`RenderDistribution` 或公开 Distribution handler；`PUB-*` 为各领域公开 handler；`LOGIN`/`SIDE` 为 `RenderLogin`/`RenderSidebar`。`E-T`=共享组件测试，`E-M`=素材 360/420/1280 视觉参考与 Radar Host 布局证据，`E-G`=群聊 360/420/1280 视觉参考，`E-PG`=GroupOps PostgreSQL/认证 Chromium Journey，`E-CH`=PR #287 渠道 Journey，`E-O`=PR #299 CI，`E-ORD`=订单截图；历史或失败 CI 不计为通过。

远程选择器的空词 Enter 必须从服务端重新请求调用方授权的无关键词分页目录；只有渠道本地过滤可以回到当前页面已加载的全量结果。选择器共享合同还需分别观察 IME 候选 Enter/Escape、取消不提交、403 保留 draft、跨页选择、`chat_reference` 与素材 key 隔离、只读和焦点、保存锁及失败后读回。

| # | 路由／页面条目（类型） | Host | assets | 组件 | 视口 | 状态 | 已测证据 | 待办 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 1 | `/` → `/admin`（根跳转） | WB | admin shell | `admin_base` | 1280/1440 | 重定向、未登录、已登录 | C0：`cmd/aicrm/composition.go` 路由核对 | 认证浏览器确认最终落点和导航 active 状态 |
| 2 | `/admin`（首页/`index.html`） | WB | admin shell | `admin_base` | 1280/1440 | loading、空、错误、403、只读 | C0 | 首页正文和 V3 导航绑定尚未有生产浏览器读回；PR #299 另计 |
| 3 | `/admin/automation-conversion`（`automation.html`） | WB | admin shell + audience assets | `admin_audience` | 1280/1440 | loading、空、错误、筛选、只读 | C0：`renderer.go` audience 分支 | 自动化运营真实数据、分页、错误和权限态逐页验收 |
| 4 | `/admin/automation-conversion/packages/{id}`（`audienceEdit.html`） | WB | admin shell + audience detail assets | `admin_audience_detail` | 1280/1440 | loading、不存在、错误、只读 | C0 | 真实方案详情读回和返回列表路径 |
| 5 | `/admin/operation-cycles`（`cycles.html`） | OP | tokens/labs/`operationCyclesHost` | 运营闭环 Host | 1280/1440 | loading、空、失败、只读、执行中 | C0：`internal/operationcycle/ui.go` | 真实周期、执行事实、复盘数据和错误态 |
| 6 | `/admin/operation-cycles/cyclesDetail.html?id={ordinal}`（`cyclesDetail.html`） | OP | 同上 | 周期详情/执行记录 | 1280/1440 | loading、不存在、失败、只读 | C0 | 详情和执行记录服务端 readback；`cycles.html` 旧 alias 只应重定向 |
| 7 | `/admin/automation-conversion/group-ops/ui` → `/admin/groupops.html`（alias carrier） | GRP | groupops bundle | GroupOps list Host | 1280/1440/420/360 | 重定向、未登录、loading | C0：`groupops/ui.go` | 认证浏览器确认 alias 不产生第二套页面 |
| 8 | `/admin/automation-conversion/group-ops/groups/ui` → `/admin/groupops.html`（groups alias） | GRP | groupops bundle | Group directory entry | 1280/1440/420/360 | 重定向、loading、403 | C0 | alias 的 scope、布局和权限读回 |
| 9 | `/admin/groupops.html`（active list） | GRP | groupops/standard picker/composer/readonly | 群运营计划列表 | 1280/1440 | loading、空、错误、403、只读 | E-T；C0 | PR #300 已 Ready for review、未合入；CI `34892980966` 的 plan/preflight/frontend/browser/backend/archive/check 全通过；旧 browser run `34889971717` 失败 |
| 10 | `/admin/groupopsDetail.html?id={id}`（`groupopsDetail.html` active） | GRP | 同上 | 计划详情、群聊选择器、素材选择器、内容 composer | 1280/1440/420/360 | loading、404、403、部分失败、保存中、保存后 readback | E-T、E-G；冻结合同含 `chat_reference`/Owner scope/保存锁/读回 | 新认证 Journey 需验证 renderer refresh ready、节点/负责人/群绑定读回；截图只作视觉参考 |
| 11 | `/admin/groupopsDetail.html?id={id}&history=1`（history） | GRP | readonly + donor history bundle | 只读历史详情 | 1280/1440 | loading、空、失败、只读 | C0 | 历史事实和只读操作逐页浏览器验收 |
| 12 | `/admin/automation-conversion/group-ops/plans/{id}` → detail（canonical API-shaped alias） | GRP | groupops bundle | 详情 Host | 1280/1440 | 重定向、未登录、不存在 | C0 | 确认 alias 保留 query/授权且不绕过 canonical detail |
| 13 | `/admin/channels`（`channels.html`） | CH | channel + standard CSS/Host | 渠道码列表、提交式本地筛选 | 1280/1440 | loading、空、错误、403、历史只读 | E-CH：PR #287 run `34876725254`；未合入 | 以 main 合入后的认证页面复测；提交式搜索之外的业务状态仍待验收 |
| 14 | `/admin/channels/new`（`channelForm.html` new） | CH | 同上 | 渠道表单、群聊/标签/素材入口 | 1280/1440 | 草稿、校验失败、403、保存成功/失败 | C0 | 真实创建回读、取消不提交和外部效果前置条件 |
| 15 | `/admin/channels/{id}/edit`（`channelForm.html` edit） | CH | 同上 | 渠道编辑、客服/标签/素材 | 1280/1440 | loading、404、脏表单、保存失败、只读 | C0 | 真实编辑读回、权限边界和 Provider receipt |
| 16 | `/admin/cloud-orchestrator/plans`（`aiassistant/list.html`） | AI | AI host + group/material/composer/readonly | AI 助手计划列表 | 1280/1440 | loading、空、错误、403、只读 | C0：`aiassistant/ui.go` | AI 计划真实数据、审阅状态和执行事实逐页验收 |
| 17 | `/admin/cloud-orchestrator/plans/{id}`（`aiassistant/detail.html`） | AI | 同上 | AI 计划详情、审阅/执行 readback | 1280/1440 | loading、404、审阅中、失败、outcome_unknown | C0 | 真实审阅回读与 External Effects receipt；不能把 queued 当 executed |
| 18 | `/admin/cloud-orchestrator/campaigns`（reserved placeholder） | WB | admin shell | 受控 placeholder | 1280/1440 | unavailable、403、只读 | C0：webshell admin spec | 需确认是否保留入口或绑定实际 Campaign Host，不能按 AI 计划页通过 |
| 19 | `/admin/cloud-orchestrator/observability`（reserved placeholder） | WB | admin shell | 受控 placeholder | 1280/1440 | unavailable、错误、只读 | C0 | 观测真实 projection 和状态颜色逐页验收 |
| 20 | `/admin/external-effects` → `/admin/campaigns.html?view=external-effects` | WB/External Effects | tokens/labs/admin | External Effects shell | 1280/1440 | 重定向、loading、空、错误、outcome_unknown | C0：`externaleffects/ui.go` | 外部效果只读事实、对账和 job query 的认证浏览器 readback |
| 21 | `/admin/campaigns.html?view=external-effects&job={id}`（campaign artifact） | WB/External Effects | 同上 | 外部效果详情/历史 | 1280/1440 | loading、404、accepted/attempted/unknown/reconciled | C0 | 逐项绑定 execution status 和 receipt；不把历史 CI 当页面证据 |
| 22 | `/admin/message-archive`（archive list） | WB | admin shell + archive assets | 客户/会话存档列表 | 1280/1440 | loading、空、错误、403、只读 | C0：archive route mount | 认证客户选择、已入库消息 readback 和 PII 脱敏 |
| 23 | `/admin/message-archive/customers/{id}`（archive detail） | WB | 同上 | 客户会话详情 | 1280/1440 | loading、无记录、错误、403、只读 | C0 | Customer/Identity 归属与时间线真实数据验收 |
| 24 | `/admin/customers`（`customers.html`） | WB | admin shell + customer Host | 客户列表、筛选、分页 | 1280/1440 | loading、空、错误、403、只读 | C0：`renderer.go` customers 分支 | OneID/客户 API 认证读回、分页和手机号脱敏 |
| 25 | `/admin/customers/{id}`（`customerDetail.html`） | WB | admin shell + customer assets | 客户档案与历史入口 | 1280/1440 | loading、404、冲突、错误、只读 | C0 | Customer 主键、Identity assurance、订单/问卷/存档分区逐页读回 |
| 26 | `/admin/user-ops/ui`（reserved placeholder） | WB | admin shell | 漏斗/用户运营 placeholder | 1280/1440 | unavailable、403、只读 | C0：route registry | 不得把 HXC 或 overview 证据套用到此页；确认真实 Host |
| 27 | `/admin/hxc-dashboard`（HXC dashboard） | WB/HXC | tokens/labs/hxc admin | HXC 投影 | 1280/1440 | loading、空、错误、403、只读 | C0：HXC UI binding | 真实投影、指标来源、筛选和错误态 |
| 28 | `/admin/hxc-send-config`（reserved config） | WB | admin shell | 受控 placeholder | 1280/1440 | unavailable、403、只读 | C0 | 绑定或明确下线此 route；不与自动化话术混验 |
| 29 | `/admin/questionnaires`（`questionnaires.html`） | SUR | survey host + standard tag CSS | 问卷列表、提交式筛选 | 1280/1440 | loading、空、错误、403、历史只读 | C0；组件合同未接入 | 真实问卷/版本/答卷 readback 和筛选状态 |
| 30 | `/admin/questionnaireDetail.html`（`questionnaireDetail.html` new） | SUR | editor + survey assets | 问卷编辑器 | 1280/1440 | 草稿、校验失败、取消、保存失败 | C0 | 编辑器真实保存回读、预览不触发发送、素材/标签授权 |
| 31 | `/admin/questionnaireDetail.html?id={id}`（edit） | SUR | 同上 | 问卷编辑器/版本 | 1280/1440 | loading、404、脏表单、冲突、只读 | C0 | 版本和题目数据真实 readback |
| 32 | `/admin/questionnaireDetail.html?mode=assessment`（assessment） | SUR | survey host + editor | 评估只读/结果视图 | 1280/1440 | loading、空、失败、只读 | C0 | 评估数据、权限和无答卷态 |
| 33 | `/admin/questionnaireOps.html?id={id}`（`questionnaireOps.html`） | SUR | survey host + standard CSS | 问卷运营/历史 | 1280/1440 | loading、空、错误、403、只读 | C0 | 外部效果回执与历史 readback；不把问卷列表证据复用为完成 |
| 34 | `/admin/questionnaires/new`（registry route） | SUR | survey assets | 预期 questionnaire detail carrier | 1280/1440 | 404/redirect、未登录 | C0：route registry；当前 `surveyPage` 未接受该路径 | 修正 canonical route 或明确失效，避免导航指向未挂载页 |
| 35 | `/admin/radar-links`（`radar.html`） | RAD | radar + standard Host | 内容雷达列表 | 1280/1440 | loading、空、错误、403、只读 | C0；E-M/E-R 只覆盖 picker 布局 | 雷达链接真实列表、统计 projection、权限和状态 |
| 36 | `/admin/radarForm.html`（`radarForm.html` new） | RAD | 同上 | Radar form + material picker | 1280/1440 | 草稿、候选 Enter、Escape、素材 403、保存失败 | E-M、E-R；当前仅 legacy 单项 callback | 真实 `selectedRecords/onCommit` 多选、移除、重开回显和业务保存/失败读回 |
| 37 | `/admin/radarForm.html?id={id}`（edit） | RAD | 同上 | Radar edit + material picker | 1280/1440 | loading、404、已有素材、取消、错误、只读 | C0；无真实多选业务 evidence | 回显实际素材记录、删除/替换、保存后服务端 readback |
| 38 | `/admin/radarDetail.html?id={id}`（`radarDetail.html`） | RAD | radar + standard Host | 雷达详情、访客/统计 | 1280/1440 | loading、无事件、错误、403、只读 | C0 | 统计可用/不可用和访客归因逐页验证；OneID assurance 不由页面自报 |
| 39 | `/admin/radar-links/new`（registry route） | RAD | radar assets | 预期 Radar form carrier | 1280/1440 | 404/redirect、未登录 | C0：registry；当前 `radarPage` 未接受该路径 | 修正 canonical route 或导航，不能把 `radarForm.html` 证据套过来 |
| 40 | `/admin/wecom-tags`（`tags.html`） | WB/Tags | tags admin + tokens/labs | 标签目录/本地同步意图 | 1280/1440 | loading、空、错误、403、只读 | C0：Tag UI binding | 真实目录、同步 receipt、客户标签命令和权限 |
| 41 | `/admin/wechat-pay/transactions`（reserved transaction view） | WB | admin shell | 受控不可用态 | 1280/1440 | unavailable、403、只读 | C0：webshell spec；订单 Host canonical 是 `/admin/orders` | 明确 placeholder 与交易页边界；不得声称订单验收 |
| 42 | `/admin/orders`（`orders.html`） | ORD | order host + tokens/labs | 订单列表、筛选 | 1280/1440 | loading、空、错误、403、只读、unknown | E-ORD 仅有局部截图；PR #301 CI frontend 失败 | 修复 CI 后逐页认证浏览器和订单快照 readback；不把 outcome_unknown 当失败或到账 |
| 43 | `/admin/orderDetail.html?id={order_ref}`（`orderDetail.html`） | ORD | order host | 订单详情、分销事实 | 1280/1440 | loading、404、退款中、unknown、只读 | E-ORD：1280 success/1440 unknown 截图 | overview 同指标下钻未实现；文案使用“系统分账成功确认时间” |
| 44 | `/admin/wechat-pay/products`（`products.html`） | PROD | product host + standard CSS/Host | 普通商品列表 | 1280/1440 | loading、空、错误、403、只读 | C0：Product UI binding | 商品真实列表、分页、配置状态和选择器调用页 |
| 45 | `/admin/wechat-pay/products/new`（`productForm.html` new） | PROD | 同上 | 普通商品表单、分销配置 | 1280/1440 | 草稿、校验失败、保存失败、只读 | C0；分销配置 PR 另计 | 保存后商品快照、分销开关/比例/等待期 readback |
| 46 | `/admin/wechat-pay/products/{id}/edit`（`productForm.html` edit） | PROD | 同上 | 普通商品编辑 | 1280/1440 | loading、404、脏表单、冲突、只读 | C0 | 真实编辑 readback；不放分销员申请入口 |
| 47 | `/admin/service-period-products`（`spProducts.html`） | PROD | product host + standard CSS/Host | 周期商品列表 | 1280/1440 | loading、空、错误、403、只读 | C0 | 周期商品真实数据、分页和入口 |
| 48 | `/admin/service-period-products/new`（`spProductForm.html` new） | PROD | 同上 | 周期商品表单 | 1280/1440 | 草稿、校验失败、保存失败、只读 | C0 | 真实保存/发布 readback |
| 49 | `/admin/service-period-products/{id}/edit`（`spProductForm.html` edit） | PROD | 同上 | 周期商品编辑 | 1280/1440 | loading、404、脏表单、冲突、只读 | C0 | 真实详情与会员数据入口 |
| 50 | `/admin/spProductData.html?id={id}`（member grid；aliases `/admin/wechat-pay/spProductData.html`, `/admin/wechat-pay/products/spProductData.html`, `/admin/service-period-products/spProductData.html`） | PROD/member-grid | product member-grid assets | 会员数据表格 | 1280/1440 | loading、空、错误、403、只读、分页 | C0：`mountMemberGridUI` | 共享页授权、分页和真实会员 projection readback |
| 51 | `/admin/coupons`（`coupons.html`） | COUP | coupon host + tokens/labs | 优惠券规则列表 | 1280/1440 | loading、空、错误、403、只读 | C0 | 规则、领取事实和核销快照真实读回 |
| 52 | `/admin/couponForm.html`（`couponForm.html` new） | COUP | 同上 | 优惠券表单 | 1280/1440 | 草稿、校验失败、保存失败、只读 | C0 | 创建后 slug/规则 readback |
| 53 | `/admin/couponForm.html?id={id}`（edit；alias `/admin/coupons/{id}/edit`） | COUP | 同上 | 优惠券编辑 | 1280/1440 | loading、404、脏表单、冲突、只读 | C0 | alias/canonical 一致性与保存后事实 |
| 54 | `/admin/couponData.html?id={id}`（`couponData.html`） | COUP | coupon host | 领取数据/核销事实 | 1280/1440 | loading、空、错误、403、只读、分页 | C0 | 领取记录和客户归因真实 readback |
| 55 | `/admin/alipay/transactions`（reserved transaction view） | WB | admin shell | 受控不可用态 | 1280/1440 | unavailable、403、只读 | C0：route registry | 明确支付域边界；没有真实支付宝页面证据前保持未验收 |
| 56 | `/admin/image-library`（`images.html`） | MED | media + material save/filter Host | 图片素材库 | 1280/1440 | loading、空、错误、403、只读、上传失败 | C0；素材 shared tests 不等于库页面 | 本地素材、私有 blob、缩略图失败 fallback 和保存回读 |
| 57 | `/admin/miniprogram-library`（`mpLib.html`） | MED | media bundle | 小程序素材库 | 1280/1440 | loading、空、错误、403、只读 | C0 | 真实元数据、缩略图失败和素材状态 |
| 58 | `/admin/attachment-library`（`attach.html`） | MED | media bundle | 附件素材库 | 1280/1440 | loading、空、错误、403、只读 | C0 | MIME/类型隔离、下载/预览状态和审计 |
| 59 | `/admin/automation-agents`（`agents.html`） | AUT | tokens/labs/admin | 自动化话术列表 | 1280/1440 | loading、空、错误、403、只读 | C0：`internal/automation/ui.go` | 与 GroupOps/运营闭环分开验收；真实 Agent/fixed_script 数据和状态 |
| 60 | `/admin/agentEdit.html`（`agentEdit.html` new） | AUT | automation bundle | 自动化话术表单 | 1280/1440 | 草稿、校验失败、保存失败、只读 | C0 | create code、保存后读回和取消不提交 |
| 61 | `/admin/agentEdit.html?id={id}`（edit） | AUT | automation bundle | 自动化话术编辑 | 1280/1440 | loading、404、脏表单、冲突、只读 | C0 | 真实更新、type/saved query 和审计 |
| 62 | `/admin/owner-migration`（`ownerMig.html` action） | WB/Owner Handoff | admin shell + owner Host | 负责人迁移预览/提交 | 1280/1440 | 草稿、校验失败、403、部分失败、读回 | C0：`mountOwnerHandoffUI` | 本地/企微受理、冻结预览和最终接替 readback |
| 63 | `/admin/ownerMig.html?contact_history=1`（legacy history） | WB | admin shell + history assets | 负责人联系历史只读 | 1280/1440 | loading、空、错误、只读 | C0 | 保持只读，不加载 mutation-capable Host |
| 64 | `/admin/config`（`config.html`/runtime center） | CFG-V3 | runtime config Host | 配置中心 | 1280/1440 | loading、空、错误、403、只读 | C0：`configPage` | 按既有分类权限验收；安全配置仅顶级超管，另核对配置版本和浏览器 readback |
| 65 | `/admin/configDetail.html?cat={category}`（`configDetail.html`） | CFG-V3 | runtime config Host | 配置分类 | 1280/1440 | loading、未知分类、校验失败、只读 | C0 | 逐分类真实配置、保存草稿/应用 revision readback |
| 66 | `/admin/config/releases`（runtime release list） | CFG-V3 | runtime config Host | 发布列表 | 1280/1440 | loading、空、错误、403、只读 | C0 | 发布状态、进程 revision 和应用事实 |
| 67 | `/admin/config/releases/new`（runtime release new） | CFG-V3 | runtime config Host | 发布草稿 | 1280/1440 | 草稿、校验失败、冲突、失败读回 | C0 | 真实发布前后 readback；不能以保存草稿代替应用 |
| 68 | `/admin/config/releases/{id}`（runtime release detail） | CFG-V3 | runtime config Host | 发布详情 | 1280/1440 | loading、404、unknown、只读 | C0 | 应用/回滚事实和 revision 绑定 |
| 69 | `/admin/config/app-settings`（reserved config alias） | WB/CFG-D | admin shell 或 config donor | 旧配置入口 | 1280/1440 | unavailable、redirect、403 | C0：registry；未见独立 configPage | 确认 canonical redirect/placeholder，禁止按配置中心通过 |
| 70 | `/admin/config/login-access`（access page） | WB/Access | admin shell + access Host | staff login permissions | 1280/1440 | loading、403、校验失败、保存失败、只读 | C0：webshell `admin_access` | 仅顶级超管、角色迁移/服务端 enforcement/浏览器读回 |
| 71 | `/admin/api-docs`（`apidocs.html`） | CFG-D/OpenPlatform | config donor + open platform assets | API 文档/调用方管理 | 1280/1440 | loading、空、错误、403、只读 | C0：OpenPlatform UI mount | 凭证不进入日志，调用方保存/撤销和权限 readback |
| 72 | `/admin/oneid`（reserved unavailable） | WB | admin shell | 明确不可用态 | 1280/1440 | 404、未登录 | C0：composition 明确 `NotFoundHandler` | 保持不可用；OneID 只能通过后台 Port/API 验收 |
| 73 | `/login`（login page） | LOGIN | login assets | 登录表单 | 1280/1440/390 | 初始、CSRF 失败、凭据失败、WeCom 入口 | C0：`RenderLogin` | 认证浏览器确认 Cookie/redirect/错误信息，不记录凭据 |
| 74 | `/auth/wecom/start`（redirect route） | LOGIN/WeCom | auth provider | OAuth 起始跳转 | 390/430 | disabled、失败、回跳 | C0：route mount | Provider 验证、state/回跳和 cookie 读回 |
| 75 | `/logout`（session route） | Access | none | 会话终止 | 390/430 | 已登录、重复、CSRF/方法错误 | C0 | 认证浏览器确认退出后受保护页面不可读 |
| 76 | `/sidebar/bind-mobile`（sidebar `index.html`） | SIDE | sidebar assets | 企微 sidebar shell | 360/420 | loading、未绑定、绑定失败、已绑定、只读 | C0：`RenderSidebar` | 真实企微宿主、context token/JSSDK/API 授权与绑定 readback |
| 77 | `/distribution`（public distribution center） | DIST/PUB-DIST | distribution public CSS/JS | 分销员中心 | 375/390/430 | loading、未授权、空、错误、settlement unknown | C0：`mountDistribution` | 支付派生 session、归因/佣金真实数据和写入回执 |
| 78 | `/admin/distribution`（distribution admin） | DIST | distribution CSS/detail drawer/admin JS | 分销管理 | 1280/1440 | loading、空、错误、403、只读、异常 | C0：`RenderDistribution` | 订单快照、复核期、预计/实际结算、异常证据和下钻 |
| 79 | `/r/{public_code}`（Radar public viewer） | PUB-RAD | Radar public handler | 内容雷达公开页 | 375/390/430 | loading、授权中、已授权、失效、404、内容失败 | C0：`internal/radar/http/handler.go` | 公开页认证/UnionID assurance、内容读取和 provider receipt |
| 80 | `/p/{code}`（public product detail） | PUB-PROD | product public handler | 商品详情 | 375/390/430 | loading、空、失效、已购买、错误 | C0：public product mount | 公开真实商品数据和购买状态读回 |
| 81 | `/pay/{code}`（public product checkout） | PUB-PROD | product public handler | 商品结算 | 375/390/430 | 授权中、可购买、已购买、失败、outcome_unknown | C0 | 支付幂等、原订单保留和 provider receipt；不把页面成功当到账 |
| 82 | `/s/{code}`（service-period detail） | PUB-SERVICE | service-period public handler | 周期商品详情 | 375/390/430 | loading、失效、已购买、错误 | C0 | 周期商品与会员权益 readback |
| 83 | `/s/{code}/pay`（service-period checkout） | PUB-SERVICE | service-period public handler | 周期商品结算 | 375/390/430 | 授权中、可购买、失败、unknown | C0 | 原订单恢复、幂等和支付 receipt |
| 84 | `/c/{slug}`（coupon public claim） | PUB-COUP | coupon public handler | 优惠券详情/领取 | 375/390/430 | loading、失效、已领取、不可用、错误 | C0 | 领取事实、客户归属和重复领取反馈 |
| 85 | `/q/{key}`（public survey share entry） | PUB-SUR | survey public handler | 问卷分享入口 | 375/390/430 | loading、需授权、过期、错误、已完成 | C0：survey API/UI mount | 真实问卷、OAuth、提交和结果查询 readback |
| 86 | `/shared/service-period-member-grid`（shared member grid） | PUB-SERVICE/member-grid | member-grid assets/icons | 会员数据共享表格 | 375/390/430 | loading、空、错误、过期、只读、分页 | C0：`mountMemberGridUI` | token scope、分页和过期链接真实浏览器验收 |
| 87 | `/h5/index.html`（H5 index artifact） | PUB-SUR | H5 entry + survey-assets | H5 构建入口／分流页 | 375/390/430 | loading、空、错误、回跳 | C0：`web/scripts/build.mjs` 生成 `h5/index.html` | 移动端入口、分流和授权回跳浏览器验收 |
| 88 | `/h5/auth.html`（H5 auth artifact） | PUB-SUR | H5 + survey-assets | 授权页 | 375/390/430 | loading、授权失败、回跳 | C0：`survey.PublicUIBinding` | H5 origin、OAuth state 和移动端浏览器 |
| 89 | `/h5/all.html`（H5 all artifact） | PUB-SUR | H5 + survey-assets | 全量答题页 | 375/390/430 | loading、空、错误、提交中、完成 | C0 | 题目/提交/结果 readback |
| 90 | `/h5/one.html`（H5 one artifact） | PUB-SUR | H5 + survey-assets | 单题答题页 | 375/390/430 | loading、校验失败、提交中、完成 | C0 | 移动端焦点、校验和提交回执 |
| 91 | `/h5/loading.html`（H5 loading artifact） | PUB-SUR | H5 + survey-assets | 加载态 | 375/390/430 | 初始、超时、重试 | C0 | 真实网络延迟与错误转场 |
| 92 | `/h5/error.html`（H5 error artifact） | PUB-SUR | H5 + survey-assets | 错误态 | 375/390/430 | 404、403、网络失败、重试 | C0 | 错误原因和重试不重复提交 |
| 93 | `/h5/result.html`（H5 result artifact） | PUB-SUR | H5 + survey-assets | 结果页 | 375/390/430 | loading、空结果、错误、只读 | C0 | 结果查询与授权边界 |
| 94 | `/h5/done.html`（H5 done artifact） | PUB-SUR | H5 + survey-assets | 完成页 | 375/390/430 | 完成、重复打开、错误 | C0 | 完成事实 readback，不重复写入 |
| 95 | `/h5/signup.html`（H5 signup artifact） | PUB-SUR | H5 + survey-assets | 注册/采集页 | 375/390/430 | loading、校验失败、重复、完成 | C0 | OneID/identity 证明和提交回执 |
| 96 | `/h5/active.html`（H5 active artifact） | PUB-SUR | H5 + survey-assets | 有效问卷页 | 375/390/430 | loading、答题、提交失败、完成 | C0 | 真实活动问卷数据和提交 readback |
| 97 | `/h5/expired.html`（H5 expired artifact） | PUB-SUR | H5 + survey-assets | 过期页 | 375/390/430 | 过期、重试、只读 | C0 | 过期链接不得提交 |
| 98 | `/h5/pay.html`（H5 pay artifact） | PUB-SUR | H5 + survey-assets | 问卷支付页 | 375/390/430 | 授权中、可支付、失败、unknown | C0 | 支付 session、幂等和 provider receipt |
| 99 | `/h5/qr.html`（H5 QR artifact） | PUB-SUR | H5 + survey-assets | QR/分享页 | 375/390/430 | loading、生成失败、过期、只读 | C0 | 真实二维码/链接和过期策略 |

矩阵当前覆盖的是 clean main 能确认的 route/view 与既有 artifact；`C0`、共享组件测试、注入式 Host 或单一页面截图都不会自动升级为 `C3`。未完成行必须在对应真实 Host、授权数据、失败态和服务端 readback 完成后单独更新证据，GroupOps 标准群运营、运营闭环、自动化话术、AI 助手四类页面也必须保持独立记录。
