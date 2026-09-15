# 前端真实组件索引（全局 UI 治理核对，2026-09-15）

以当前 main `0e73d42c433892a1f5088eea08d3a079c1c98455` 的实际仓库路径为准。索引用于选择现有入口，不授权跨领域读取或写入；动工前除路径外，还要追踪 canonical 路由、handler／adapter、`Render*` 或挂载、manifest assets 和页面调用。链路固定为“canonical route → handler／领域 UI adapter → `Render*` 或对应 mount → manifest asset → page caller”。未挂载实现、历史快照和构建产物不能单独作为当前标准；冻结 donor 只作行为／视觉证据，禁止修改。

| 场景 | 入口 | 调用／装配 | 边界 |
| --- | --- | --- | --- |
| 管理端单壳 | `internal/webshell/templates/admin_base.html` | `internal/webshell/renderer.go` 的 `RenderDistribution` | 仅一个 `.admin-sidebar` 和 `.admin-topbar`；分销领域只提供经 manifest 验证的正文 assets。 |
| 问卷管理与编辑器 | `web/v3/surveyAdapter.ts`、`web/src/admin/sections/questionnaireEditor.ts` | `internal/survey/ui.go` 解析 manifest，`RenderSurvey` 在冻结 admin bundle 前装配列表 bridge；编辑器由 `questionnaireEditor` 入口挂载 | 一级列表复用冻结 table、检索和操作；bridge 仅映射问卷名称。编辑页保留标题用于答题/预览，状态以服务端 readback 为准。 |
| 通用详情抽屉 | `web/v3/shared/ui/detailDrawer.ts`、`web/v3/shared/ui/detailDrawer.css` | `distributionAdmin.ts` 导入行为，`sharedDetailDrawerStyles` 由 `admin_base` 挂载 | 组件只负责焦点回收、Escape/关闭和视觉容器；调用方必须读取自己的受权服务端事实，不能由抽屉伪造状态。 |
| 商品编辑分销配置（售卖信息唯一配置） | `web/v3/productAdapter.ts`、`web/v3/productDistribution.css` | Product UI 的 `ProductCSS` 经 `admin_base` 装配 | 售卖信息只保留本商品分销启用开关、比例和退款复核等待天数；素材、购买后动作、企微标签、外部推送等业务板块保留但不显示分销配置；商品编辑页不挂分销员申请入口、链接、复制按钮或二维码。 |
| 分销管理详情 | `web/v3/distributionAdmin.ts`、`web/v3/distribution.css` | `RenderDistribution` 的 `#distribution-admin-root` 和 Distribution manifest assets | 只使用现有后台 table、tab、button token；公共分销中心样式必须限定在 `body[data-ui-surface="distribution"]`，不能重置后台全局样式。 |

## 全局统一 UI 增补清单（2026-09-15）

本节登记当前 main 的实际 Host、授权读取和未完成合同。#317 已于 `2026-09-15T03:29:19Z` squash 合入 main；其 review head `09cf5d7…` 与当时 main `84c34e5…` 的 tree 均为 `6838f977…`，包含 #287、#296、#300、#303、#305、#307、#310、#311、#315 的能力。随后 #324 以 merge commit `0e73d42c433892a1f5088eea08d3a079c1c98455` 合入 Radar 管理列表筛选分页，run `34926594842` 必跑 lane 全 PASS、deploy SKIPPED；本工作树已同步该最新 main。线上 IAB 仍无认证读回。未合入或未真实页面验收的调用不得因共享模块存在而标覆盖。商品编辑分销行由商品专项 PR 单独维护，避免与该专项并行修改。

| 场景 | 入口 | 调用／装配 | 已核实状态与边界 |
| --- | --- | --- | --- |
| 管理端全局反馈 | `web/v3/surfaceFeedbackHost.ts` | 构建脚本向生成的 admin、H5、sidebar 与分享文档注入反馈 Host | 只映射加载／资源失败表现；不把业务失败伪装成成功，也不改变领域命令。 |
| 已提交文本搜索，第一批 | `web/v3/shared/ui/committedTextSearch.ts` | 渠道：`channelCenterAdapter.ts`；标准选择器：`standardComponentsHost.ts`；其他生成页面：`surfaceFeedbackHost.ts` | **#287 能力已随 #317 进入 main。** 只精确注册渠道、优惠券、问卷、标签、计划、素材库与标准群聊／标签／客服／素材选择器控件；不全局拦截表单。渠道真实 Chromium 输入法 Journey 由 run `34876725254` 通过，其他匹配页面仍须逐页业务验收；普通 Enter／空词回到调用方授权目录，composition 候选 Enter 不触发查询。 |
| 标签选择器 | 冻结 `wecom_tag_picker.js`；V3 `standardComponentsHost.ts` 与 `web/v3/shared/ui/tagPickerAdapter.ts` | 当前真实 V3 caller 为 Channel、Customer、Product；各自 Host 提供页面授权的目录 loader 与 commit 回调 | #305 能力已随 #317 进入 main；共享合同覆盖分组、搜索、单／多选、回显、失效、403、刷新和失败保留。`/admin/component-states` 尚无 Tag 独立状态示例，选择器不替客户打标；问卷等未列 caller 不得套用。 |
| 客服／员工选择器 | 冻结 `operation_member_picker_dd8d60d.js`；V3 `standardComponentsHost.ts` 与 `web/v3/shared/ui/staffPickerAdapter.ts` | Channel 与 GroupOps 使用 V3 adapter；兼容 Host 仍负责冻结员工 picker 的加载与方法捕获 | #311 能力已随 #317 进入 main；Channel 有真实授权 scope、刷新和保存前草稿测试。`/admin/component-states` 尚无 Staff 独立状态示例；非群运营调用不能套用 GroupOps scope，Customer／owner migration 仍需真实页面验收。 |
| 真实群聊选择器 | 冻结 `group_chat_picker.js`；V3 `selectionSession.ts`／`groupPickerAdapter.ts` | #300 能力已随 #317 进入 main；GroupOps 使用 V3 session，其他页面仍由各自 Host 决定是否挂载 | Owner scope 分页、失效回显、`chat_reference`、保存锁和保存读回由 #300 分支 CI `34892980966` 覆盖；该 run 没有部署。群邀请素材命令不在此项，其他调用不得套用 GroupOps 证据。 |
| 素材选择器：Radar | `web/v3/shared/ui/selectionSession.ts`、`materialPickerAdapter.ts`、`radarAdapter.ts` | #296 能力已随 #317 进入 main；`RenderRadar` 的 `RadarAssets.StandardHostJS` 加载标准 Host，Radar 注入图片／附件目录 loader | adapter 状态测试和 Radar renderer callback Journey 只证明单项添加与弹窗行为；Radar owner 只允许一个 image 或 PDF `media_item_id`，仍缺真实单选／替换／移除／重开回显和业务 save/GET。组件不读取 `AdminApi`、不扩大目录 scope。 |
| 素材选择器：产品及其他调用 | `web/v3/productAdapter.ts`、冻结素材 picker | `RenderProducts` 的 `ProductAssets.StandardHostJS` 与 Product Host | 当前产品仍为单项 legacy callback；不得写成已迁移 V3 material session。后续迁移须由调用方提供受权 `loadPage`、实际 `selectedRecords` 与 `onCommit`。 |
| 话术／内容编辑器 | 冻结 `send_content_composer.js` 与 V3 `standardComponentsHost.ts`；V3 `contentComposer.ts`／`contentPresentation.ts` | #307/#310 能力已随 #317 入 main；GroupOps 与 `excelBatches.ts` 使用 V3 composer／readonly presentation | 共享合同含编辑、素材排序、预览和只读展示，预览不能触发发送；`/admin/component-states` 尚无 Composer 独立状态示例。#322 固定话术的本地 PostgreSQL Chromium 授权保存+GET readback 已通过，但 browser/check 的 stage manifest 仍 FAIL，最终 CI、发布和线上读回未完成。 |
| 经营首页与导航 | overview 的 `overviewAdmin.ts`、`navigationHost.ts`、`admin-navigation.v3.json` | API 由 [PR #293](https://github.com/qianlan33333-png/AI-CRM-v3/pull/293) `de567042885a364adc36037b20fa733ea8a02e82` 单独记录；正文与导航由 [PR #299](https://github.com/qianlan33333-png/AI-CRM-v3/pull/299) `faa906b600517ca983af256900c8d91f9396f14b` 装配 | PR #299 CI 已通过；尚无合入、部署或生产读回，不能写成已发布。 |
| 订单分销事实展示 | 交易 Order Host 与 distribution Stable Read Port | PR #301 当前 head `961173d…`；run `34914087652` 全必跑 lane PASS，分支 `DIRTY` 未合入 | 详情只读取订单级快照、复核期、预计／实际结算及异常证据；不得读取商品当前比例或跨领域写入。#326 的 synthetic fixture 未安装 Order distribution reader，不能代替真实链路。 |

未列为「已核实」的页面不因路径相似而复用上表读取范围或命令。每次迁移更新本表时记录 canonical 路由、实际 Host／asset、数据来源和授权范围、选择回调、状态验收及尚未完成合同项。

## 共享组件状态示例与调用清单（2026-09-15）

`/admin/component-states` 是当前 main 中由 `internal/webshell/handler.go` → `Renderer.RenderComponentStates` → manifest 的 `componentStatesStyles`／`componentStatesHost` 挂载的认证后台状态示例页。它只使用本地内存 fixture，不保存业务数据、不读取 Provider；PR #317 的 run `34924244166` 必跑 lane 全 PASS，不能因此升级任何真实业务页面。

| 组件 | V3-owned 入口 | 当前真实调用 | 状态示例 | 当前级别与缺口 |
| --- | --- | --- | --- | --- |
| SelectionSession／dialog | `web/v3/shared/ui/selectionSession.ts`、`selectionDialog.ts` | Group、Material、Tag、Staff adapter 共用；调用方持有目录读取和 commit | component-states 覆盖 Group／Material 的 loading、empty、error、forbidden、readonly、invalid，以及表单／IME Enter 和焦点回收 | C1 合同＋C2 挂载／视口；不证明业务数据或保存成功 |
| Group | `web/v3/shared/ui/groupPickerAdapter.ts` | `groupOpsHostAdapter.ts`、`componentStatesHost.ts` | 本地 Group loader 与 #300 GroupOps 测试 | C3 仅限 #300 的 GroupOps 子旅程；其它页面保持 C0，不能套用 Owner scope |
| Material | `web/v3/shared/ui/materialPickerAdapter.ts` | `groupOpsHostAdapter.ts`、`radarAdapter.ts`、`productAdapter.ts`、`componentStatesHost.ts` | 本地 Material loader；#296 adapter 状态／Radar callback 证据 | C2 仅限弹窗／状态子集；Radar 只允许一个 image 或 PDF `media_item_id`，仍缺单选／替换／移除／重开回显和 save/GET；Product 等其它 caller 的多选由各自 owner 决定 |
| Tag | `web/v3/shared/ui/tagPickerAdapter.ts`、`standardComponentsHost.ts` | `channelAdmissionHost.ts`、`customerAdapter.ts`、`productAdapter.ts`；冻结 `wecom_tag_picker.js` 由 standard Host 加载 | **缺失**：component-states 尚无 Tag 状态卡片或真实 session | 共享合同 C1；每个真实 caller 按自己的 source／scope 验收，不能以 component-states 升级 |
| Staff | `web/v3/shared/ui/staffPickerAdapter.ts`、`standardComponentsHost.ts` | `channelAdmissionHost.ts`、GroupOps Host；冻结 `operation_member_picker_dd8d60d.js` 仅作 donor | **缺失**：component-states 尚无 Staff 状态卡片或真实 session | 共享合同 C1；Customer、owner migration 和非群运营范围的真实页面验收仍缺 |
| Composer／只读内容 | `web/v3/shared/ui/contentComposer.ts`、`contentPresentation.ts` | `groupOpsHostAdapter.ts`、`excelBatches.ts`；固定话术 #322 仍在独立 PR | **缺失**：component-states 尚无 Composer 编辑／预览／只读状态示例 | 共享合同 C1；预览不触发发送；#322 本地 PostgreSQL Chromium 授权保存+GET readback 已通过，但 browser/check 的 stage manifest 仍 FAIL，最终 CI、发布和线上读回未完成 |

状态示例的 C1/C2 只标示组件合同和实际挂载的证据范围。标签、客服／员工和 Composer 的状态示例仍欠；不因 `standardComponentsHost` 可加载、donor 存在或某个 CI lane 通过而把其它后台、企微 sidebar、H5 或公开 survey 页面标为完成。

## 路由／页面条目逐项验收矩阵（2026-09-15）

本矩阵以历史 clean main `3eda04cbd56d7bfdf44ba2a15d573ee29703926a` 的实际路由装配为初始盘点基线；当前 main 为 `0e73d42c433892a1f5088eea08d3a079c1c98455`，已包含 #317 的共享组件与状态示例、#318/#319/#321/#323 的后续主线修复及 #324 的 Radar 管理列表筛选分页。未合入能力的 branch head、CI 和本地证据单独记录，不能写成 main 已发布。条目总数以表中连续编号自动核算，涵盖 canonical route、alias、reserved placeholder、登录／退出和构建 artifact，不能描述为相同数量的 canonical 页面。`C0` 只表示源码路由、Host 和 assets 静态核对，不能当作页面通过；`C1` 是共享组件合同测试，`C2` 是实际挂载壳与视口证据，`C3` 才是认证业务数据和保存后读回。视口要求按页面类型执行：后台桌面 `1280/1440`、企微 sidebar `360/420`、公开或 H5 `375/390/430`。除“已测证据”明确列出的子集外，表中的“状态”统一表示“未执行/待验收”的逐项检查集合。

Host／assets 缩写：`WB`=`webshell.RenderAdmin` + `admin_base`；`CH`=`RenderChannels` + `channelCenterHost/standardComponentsHost`；`SUR`=`RenderSurvey` + `surveyHost/questionnaireEditor/standardComponentsHost`；`RAD`=`RenderRadar` + `radarHost/standardComponentsHost`；`GRP`=`RenderGroupOps` + `groupOpsHost/operationPicker/groupPicker/materialPicker/composer/readonly`；`AI`=`RenderAIAssistant` + `aiAssistantHost` 及 group/material/composer/readonly；`ORD`=`RenderOrders` + `orderHost`；`PROD`=`RenderProducts` + `productHost/standardComponentsHost`；`COUP`=`RenderCoupons` + `couponHost`；`MED`=`RenderMedia` + `materialSaveHost/imageLibraryFilterHost`；`AUT`=`RenderAutomation`；`OP`=`RenderOperationCycles` + `operationCyclesHost`；`CFG-V3`=`RenderRuntimeConfig`；`CFG-D`=`RenderConfig`；`DIST`=`RenderDistribution` 或公开 Distribution handler；`PUB-*` 为各领域公开 handler；`LOGIN`/`SIDE` 为 `RenderLogin`/`RenderSidebar`。`E-T`=共享组件合同测试，`E-M`=素材 360/420/1280 视觉参考与 Radar Host 布局证据，`E-G`=群聊 360/420/1280 视觉参考，`E-PG`=GroupOps PostgreSQL/认证 Chromium Journey，`E-CH`=#287 渠道 Journey，`E-O`=#299 本地 consumer 证据，`E-ORD`=订单／下钻截图；历史或失败 CI 不计为通过，deploy skipped 不计为发布。

远程选择器的空词 Enter 必须从服务端重新请求调用方授权的无关键词分页目录；只有渠道本地过滤可以回到当前页面已加载的全量结果。选择器共享合同还需分别观察 IME 候选 Enter/Escape、取消不提交、403 保留 draft、跨页选择、`chat_reference` 与素材 key 隔离、只读和焦点、保存锁及失败后读回。

| # | 路由／页面条目（类型） | Host | assets | 组件 | 视口 | 状态 | 已测证据 | 待办 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 1 | `/` → `/admin`（根跳转） | WB | admin shell | `admin_base` | 1280/1440 | 重定向、未登录、已登录 | C0：`cmd/aicrm/composition.go` 路由核对 | 认证浏览器确认最终落点和导航 active 状态 |
| 2 | `/admin`（首页/`index.html`） | WB | admin shell | `admin_base` | 1280/1440 | loading、空、错误、403、只读 | C0；#299 Draft、#326 仅本地下钻证据 | 首页正文和 V3 导航没有 main/生产认证读回；#293 API head `62bb9989…` 当前与 main 冲突，#299 head `7e07955b…` 无 GitHub CI |
| 3 | `/admin/automation-conversion`（`automation.html`） | WB | admin shell + audience assets | `admin_audience` | 1280/1440 | loading、空、错误、筛选、只读 | C0：`renderer.go` audience 分支 | 自动化运营真实数据、分页、错误和权限态逐页验收 |
| 4 | `/admin/automation-conversion/packages/{id}`（`audienceEdit.html`） | WB | admin shell + audience detail assets | `admin_audience_detail` | 1280/1440 | loading、不存在、错误、只读 | C0 | 真实方案详情读回和返回列表路径 |
| 5 | `/admin/operation-cycles`（`cycles.html`） | OP | tokens/labs/`operationCyclesHost` | 运营闭环 Host | 1280/1440 | loading、空、失败、只读、执行中 | C0：`internal/operationcycle/ui.go` | 真实周期、执行事实、复盘数据和错误态 |
| 6 | `/admin/operation-cycles/cyclesDetail.html?id={ordinal}`（`cyclesDetail.html`） | OP | 同上 | 周期详情/执行记录 | 1280/1440 | loading、不存在、失败、只读 | C0 | 详情和执行记录服务端 readback；`cycles.html` 旧 alias 只应重定向 |
| 7 | `/admin/automation-conversion/group-ops/ui` → `/admin/groupops.html`（alias carrier） | GRP | groupops bundle | GroupOps list Host | 1280/1440/420/360 | 重定向、未登录、loading | C0：`groupops/ui.go` | 认证浏览器确认 alias 不产生第二套页面 |
| 8 | `/admin/automation-conversion/group-ops/groups/ui` → `/admin/groupops.html`（groups alias） | GRP | groupops bundle | Group directory entry | 1280/1440/420/360 | 重定向、loading、403 | C0 | alias 的 scope、布局和权限读回 |
| 9 | `/admin/groupops.html`（active list） | GRP | groupops/standard picker/composer/readonly | 群运营计划列表 | 1280/1440 | loading、空、错误、403、只读 | E-T；C1 仅共享合同 | #300 能力已随 #317 进入 main；CI `34892980966` 的 plan/preflight/frontend/browser/backend/archive/check 全通过，deploy/quality-report skipped；仍缺发布后认证页面读回 |
| 10 | `/admin/groupopsDetail.html?id={id}`（`groupopsDetail.html` active） | GRP | 同上 | 计划详情、群聊选择器、素材选择器、内容 composer | 1280/1440/420/360 | loading、404、403、部分失败、保存中、保存后 readback | E-T、E-G；#300 的 E-PG 子旅程 | #300 能力已随 #317 进入 main；Owner scope、`chat_reference`、保存锁和保存后 readback 有分支 CI 证据；新认证 Journey、发布和其它调用页仍待验收，截图只作视觉参考 |
| 11 | `/admin/groupopsDetail.html?id={id}&history=1`（history） | GRP | readonly + donor history bundle | 只读历史详情 | 1280/1440 | loading、空、失败、只读 | C0 | 历史事实和只读操作逐页浏览器验收 |
| 12 | `/admin/automation-conversion/group-ops/plans/{id}` → detail（canonical API-shaped alias） | GRP | groupops bundle | 详情 Host | 1280/1440 | 重定向、未登录、不存在 | C0 | 确认 alias 保留 query/授权且不绕过 canonical detail |
| 13 | `/admin/channels`（`channels.html`） | CH | channel + standard CSS/Host | 渠道码列表、提交式本地筛选 | 1280/1440 | loading、空、错误、403、历史只读 | E-CH：#287 run `34876725254`；能力已随 #317 进入 main | 认证 Chromium 渠道 Journey 已验证 composition 候选 Enter 不过滤、普通 Enter 单次提交和焦点／选区保留；其它列表状态、发布读回和非渠道 caller 仍待验收 |
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
| 25 | `/admin/customers/{id}`（`customerDetail.html`） | WB | admin shell + customer assets | 客户档案与历史入口 | 1280/1440 | loading、404、冲突、错误、只读 | C0；#316 run `34914852224` 必跑 lane PASS | Customer 主键、Identity assurance、订单/问卷/存档分区逐页读回；未合入、未部署 |
| 26 | `/admin/user-ops/ui`（reserved placeholder） | WB | admin shell | 漏斗/用户运营 placeholder | 1280/1440 | unavailable、403、只读 | C0：route registry | 不得把 HXC 或 overview 证据套用到此页；确认真实 Host |
| 27 | `/admin/hxc-dashboard`（HXC dashboard） | WB/HXC | tokens/labs/hxc admin | HXC 投影 | 1280/1440 | loading、空、错误、403、只读 | C0：HXC UI binding | 真实投影、指标来源、筛选和错误态 |
| 28 | `/admin/hxc-send-config`（reserved config） | WB | admin shell | 受控 placeholder | 1280/1440 | unavailable、403、只读 | C0 | 绑定或明确下线此 route；不与自动化话术混验 |
| 29 | `/admin/questionnaires`（`questionnaires.html`） | SUR | survey host + standard tag CSS | 问卷列表、提交式筛选 | 1280/1440 | loading、空、错误、403、历史只读 | C0；组件合同未接入 | 真实问卷/版本/答卷 readback 和筛选状态 |
| 30 | `/admin/questionnaireDetail.html`（`questionnaireDetail.html` new） | SUR | editor + survey assets | 问卷编辑器 | 1280/1440 | 草稿、校验失败、取消、保存失败 | C0 | 编辑器真实保存回读、预览不触发发送、素材/标签授权 |
| 31 | `/admin/questionnaireDetail.html?id={id}`（edit） | SUR | 同上 | 问卷编辑器/版本 | 1280/1440 | loading、404、脏表单、冲突、只读 | C0 | 版本和题目数据真实 readback |
| 32 | `/admin/questionnaireDetail.html?mode=assessment`（assessment） | SUR | survey host + editor | 评估只读/结果视图 | 1280/1440 | loading、空、失败、只读 | C0 | 评估数据、权限和无答卷态 |
| 33 | `/admin/questionnaireOps.html?id={id}`（`questionnaireOps.html`） | SUR | survey host + standard CSS | 问卷运营/历史 | 1280/1440 | loading、空、错误、403、只读 | C0 | 外部效果回执与历史 readback；不把问卷列表证据复用为完成 |
| 34 | `/admin/questionnaires/new`（registry route） | SUR | survey assets | 预期 questionnaire detail carrier | 1280/1440 | 404/redirect、未登录 | C0：route registry；当前 `surveyPage` 未接受该路径 | 修正 canonical route 或明确失效，避免导航指向未挂载页 |
| 35 | `/admin/radar-links`（`radar.html`） | RAD | radar + standard Host | 内容雷达列表 | 1280/1440 | loading、空、错误、403、只读 | C0；#324 run `34926594842` 必跑 lane PASS、deploy SKIPPED；E-M/E-R 只覆盖 picker 布局 | Radar 筛选分页的认证浏览器读回、统计 projection、权限和状态仍待验收 |
| 36 | `/admin/radarForm.html`（`radarForm.html` new） | RAD | 同上 | Radar form + material picker | 1280/1440 | 草稿、候选 Enter、Escape、素材 403、保存失败 | E-M、E-R；当前仅 legacy 单项 callback | Radar 只允许一个 image 或 PDF `media_item_id`；仍需真实单选、替换、移除、重开回显和业务保存/失败读回 |
| 37 | `/admin/radarForm.html?id={id}`（edit） | RAD | 同上 | Radar edit + material picker | 1280/1440 | loading、404、已有素材、取消、错误、只读 | C0；无真实单项业务 evidence | 回显实际素材记录、删除/替换、保存后服务端 readback |
| 38 | `/admin/radarDetail.html?id={id}`（`radarDetail.html`） | RAD | radar + standard Host | 雷达详情、访客/统计 | 1280/1440 | loading、无事件、错误、403、只读 | C0 | 统计可用/不可用和访客归因逐页验证；OneID assurance 不由页面自报 |
| 39 | `/admin/radar-links/new`（registry route） | RAD | radar assets | 预期 Radar form carrier | 1280/1440 | 404/redirect、未登录 | C0：registry；当前 `radarPage` 未接受该路径 | 修正 canonical route 或导航，不能把 `radarForm.html` 证据套过来 |
| 40 | `/admin/wecom-tags`（`tags.html`） | WB/Tags | tags admin + tokens/labs | 标签目录/本地同步意图 | 1280/1440 | loading、空、错误、403、只读 | C0：Tag UI binding | 真实目录、同步 receipt、客户标签命令和权限；Tag 状态示例仍未纳入 `/admin/component-states` |
| 41 | `/admin/wechat-pay/transactions`（reserved transaction view） | WB | admin shell | 受控不可用态 | 1280/1440 | unavailable、403、只读 | C0：webshell spec；订单 Host canonical 是 `/admin/orders` | 明确 placeholder 与交易页边界；不得声称订单验收 |
| 42 | `/admin/orders`（`orders.html`） | ORD | order host + tokens/labs | 订单列表、筛选 | 1280/1440 | loading、空、错误、403、只读、unknown | E-ORD 局部截图；#301 run `34914087652` 必跑 lane PASS，branch `DIRTY` | 订单列表仍需逐页认证浏览器和订单快照 readback；不把 outcome_unknown 当失败或到账 |
| 43 | `/admin/orderDetail.html?id={order_ref}`（`orderDetail.html`） | ORD | order host | 订单详情、分销事实 | 1280/1440 | loading、404、退款中、unknown、只读 | E-ORD：1280 success/1440 unknown 截图；#326 v8 仅 synthetic fixture | Order distribution reader 未在 #326 fixture 安装，不能据此验收分销事实；overview 同指标下钻未完成，文案使用“系统分账成功确认时间” |
| 44 | `/admin/wechat-pay/products`（`products.html`） | PROD | product host + standard CSS/Host | 普通商品列表 | 1280/1440 | loading、空、错误、403、只读 | C0：Product UI binding | 商品真实列表、分页、配置状态和选择器调用页 |
| 45 | `/admin/wechat-pay/products/new`（`productForm.html` new） | PROD | 同上 | 普通商品表单、分销配置 | 1280/1440 | 草稿、校验失败、保存失败、只读 | #291 run `34898498514` attempt 2 PASS；未合入 | 售卖信息只读分销开关／比例／等待天数并做保存后商品快照 readback；其它业务板块保留，不显示分销配置 |
| 46 | `/admin/wechat-pay/products/{id}/edit`（`productForm.html` edit） | PROD | 同上 | 普通商品编辑 | 1280/1440 | loading、404、脏表单、冲突、只读 | #291 run `34898498514` attempt 2 PASS；未合入 | 真实编辑 readback；不放分销员申请入口、链接、复制按钮或二维码，Product preopen 风险另见素材消费者记录 |
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
| 59 | `/admin/automation-agents`（`agents.html`） | AUT | tokens/labs/admin | 自动化话术列表 | 1280/1440 | loading、空、错误、403、只读 | C0：`internal/automation/ui.go`；#322 branch browser/check FAIL | 与 GroupOps/运营闭环分开验收；真实 Agent/fixed_script 数据和状态；CI browser 缺 `automationContentHost` manifest entry |
| 60 | `/admin/agentEdit.html`（`agentEdit.html` new） | AUT | automation bundle | 自动化话术表单 | 1280/1440 | 草稿、校验失败、保存失败、只读 | C0 | create code、保存后读回和取消不提交；#322 本地 PG/Chrome 证据不能替代失败 CI |
| 61 | `/admin/agentEdit.html?id={id}`（edit） | AUT | automation bundle | 自动化话术编辑 | 1280/1440 | loading、404、脏表单、冲突、只读 | C0 | 真实更新、type/saved query 和审计；Prompt 与固定话术分离需独立验收 |
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
| 78 | `/admin/distribution`（distribution admin） | DIST | distribution CSS/detail drawer/admin JS | 分销管理 | 1280/1440 | loading、空、错误、403、只读、异常 | C0：`RenderDistribution`；#309 run `34907120228` 必跑 lane PASS | 订单快照、复核期、预计/实际结算、异常证据和下钻；branch `DIRTY`，未合入、未部署 |
| 79 | `/r/{public_code}`（Radar public viewer） | PUB-RAD | Radar public handler | 内容雷达公开页 | 375/390/430 | loading、授权中、已授权、失效、404、内容失败 | C0：`internal/radar/http/handler.go` | 公开页认证/UnionID assurance、内容读取和 provider receipt |
| 80 | `/p/{code}`（public product detail） | PUB-PROD | product public handler | 商品详情 | 375/390/430 | loading、空、失效、已购买、错误 | C0：public product mount；#325 仅 auth gate／mount | 公开真实商品数据和购买状态读回；未上架周期商品有效会话的只读权益组合仍待修复 |
| 81 | `/pay/{code}`（public product checkout） | PUB-PROD | product public handler | 商品结算 | 375/390/430 | 授权中、可购买、已购买、失败、outcome_unknown | C0；#325 publicCommerceJourney 仅旧 auth gate | 支付幂等、原订单保留和 provider receipt；nested UoW `503` 与 manifest `ENOENT` 修复后再验收，不把页面成功当到账 |
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

## 逐项最高证据级别（2026-09-15）

级别是当前已确认子范围的最高证据，不是整页完成标记。为避免把共享模块、CI 或截图自动升级到页面验收，矩阵 1–99 按以下索引判定；未列入 C1/C2/C3 的所有条目均为 C0。

| 条目 | 最高级别 | 证据边界 |
| --- | --- | --- |
| 9 | C1 | GroupOps 列表只记录共享合同／Host 证据；不含真实保存读回。 |
| 10、13 | C3（子旅程） | #300 GroupOps 详情的 owner scope／`chat_reference`／保存读回，以及 #287 渠道真实 PostgreSQL／Chromium composition 与 Enter Journey；不含发布或整页所有状态。 |
| 2、36、42、43 | C2（子范围） | #326 本地下钻抽屉、#296 Radar picker 布局／单项 callback、订单 1280/1440 截图；不含生产认证 readback。 |
| 1、3–8、11–12、14–35、37–41、44–61、62–99（排除上列条目） | C0 | 仅路由、Host、assets 或未完成页面边界核对；逐页真实数据、失败态、权限与 readback 尚待验收。 |

矩阵当前覆盖的是 clean main 能确认的 route/view 与既有 artifact；`C0`、共享组件测试、注入式 Host 或单一页面截图都不会自动升级为 `C3`。未完成行必须在对应真实 Host、授权数据、失败态和服务端 readback 完成后单独更新证据，GroupOps 标准群运营、运营闭环、自动化话术、AI 助手四类页面也必须保持独立记录。
