# PR06 内容包与群运营 donor 完整闭包复核

## 结论

本次复核结论：**donor 有可追溯的群运营参考闭环，但 v3 当前仍是 preparation-only，PR06 尚未达到可上线闭环**。本提交只增加审计结论和可执行门禁，不注册 HTTP，不改 Composition Root、OpenAPI、migration、deploy、lock、共享端口，不调用 Provider，也不改任何 donor 前端字节。

固定证据：

- v3 基线：19384b93fe362c7786edc81dd5595b79570f6bb1。
- donor：6bfbe5816bb89913c70adaca87d6a486260e016e。复核使用独立只读 worktree /private/tmp/aicrm-v2-pr04-donor；原始仓库的其他 HEAD 不具备证据资格。
- 当前审计分支：codex/import-groupops-audit。
- 前端闭包：35/35 个文件逐字节相同。docs/migration/groupops/pr06-donor-sha256.txt 同时有 donor SHA-256、目标 SHA-256 和 cmp 证据，scripts/check-pr06-donor-manifest.sh 可重放。
- 关联 PR05 前端硬门禁修正提交：9254a387a4f7b1ba84ba34abe0bcb94388c8c93b。该提交明确保留 donor main.ts -> legacy.ts -> AdminController 宽 bundle；不得改成 coupon-only 或其他领域专用 loader。

web/donors/groupops-v2 是 archive-only 证据，不是 v3 build 输入；v3 当前没有已装配的 Group Ops HTTP、Store、worker、Provider 或 Composition wiring。

## 1. 开发前边界分类

### 身份和领域

Group Ops 不涉及 OneID、Customer、外部客户身份、手机号、segment、audience、Campaign、收件人选择或客户标记。计划成员是本地 staff_id；群和 target_reference 是本领域不解释的 opaque 引用；素材引用是稳定的 Media 资源 ID。没有唯一可信客户证据时，Group Ops 不解析、不建客、不合并身份。

donor 的 groupOpsDirectory.ts 为保持原样仍引用生成客户端中的 p4-ai-audience operation-member edge，并不等于采用 Audience 领域。v3 只能在后端以本地 staff port/adapter 提供等价 scope=group_ops 投影，或者保持负责人选择关闭；不得修改 frozen 前端去绕过这个边缘。

### 持久化、内部任务和外部效果

当前 v3 分支仅有纯 domain、local plan service 和稳定 ports。它没有真实 Store/schema/migration、内部持久任务、目录 Provider、外部群写、发送 worker 或效果对账。因此当前提交不宣称任何计划或队列已保存、已执行或已送达。

donor 的参考实现确实包含 PostgreSQL UoW、幂等收据、审计/事件、outbound/EER、jobqueue、材料准备、Provider adapter、lease/fence 和人工对账；这些只能作为实现证据。v3 后续必须经稳定 Port 重建，Provider 默认 disabled，且不能把 accepted、queued 或 provider_accepted 当成送达。

## 2. donor 实际 build path 和前端闭包

### 实际入口

固定 donor 的 web/scripts/build.mjs 是唯一 build 证据：

1. ROOT=web、SRC=web/src、DIST=web/dist，读取 admin/registry.json 和 admin/nav.json。
2. admin bundle 的入口是 web/src/admin/main.ts，不是 Group Ops 专用入口。
3. registry 中实际生成 groupops.html（一级、nav=groupops）和 groupopsDetail.html（二级、isNav=false）。
4. build 对 registry 中的每个 screen 读取 admin/templates/<screen>.html，把模板放进静态 shell 的 #tpl，再输出 dist/admin/<screen>.html。
5. main.ts 对非 customers 页面动态加载 ./legacy；legacy.ts 静态引入 AdminController，对 Group Ops history 做 section dispatch，并在普通页面路径上执行 new AdminController(api, page)、模板 mount 和 controller init。

因此实际浏览器链是：

web/scripts/build.mjs -> web/src/admin/main.ts -> web/src/admin/legacy.ts -> AdminController -> groupops/groupopsDetail template

这个链包含 donor 更宽的旧页面依赖，但它是已冻结的 PR01 bundle。不能另写 Group Ops frontend runtime、删掉 legacy.ts 的广泛依赖、把 bundle 裁成专用 loader，或以 donor 完整 v2 shell 替换 v3 shell。v3 只允许通过 v3-owned adapter 允许 Group Ops 页面和后端 rule route。

### 35/35 文件、CSS 和 assets

web/donors/groupops-v2/src 恰好 35 个文件，manifest 的 source/target 映射逐一经过 SHA-256 与 cmp：

| 闭包 | 数量 | 实际内容 |
| --- | ---: | --- |
| 页面模板 | 2 | groupops.html、groupopsDetail.html |
| Group Ops section | 4 | groupOpsDirectory.ts、groupOpsHistory.ts、broadcastJobHistory.ts、util.ts |
| API adapter/characterization | 7 | Group Ops directory/history/broadcast API、对应 tests、admin.test.ts、capabilities |
| 生成 DTO/路径 | 7 | Group Ops、workspace、history、execution-runtime、broadcast-history、operation-member edge、health schema |
| frozen legacy bridge | 1 | admin/legacy.ts |
| PR01 shared runtime | 14 | main.ts、controller.ts、registry/nav、api/admin、transport、shared client/types/mock、feedback/picker/runtime/tokens |

没有 Group Ops 专用 CSS 文件、PNG、SVG、字体或其他业务 asset；模板和 sections 使用 inline style。tokens.css 是 PR01 shared bundle 的字节，不能产生 PR06 变体。

门禁还必须确认：

- v3 internal/webshell/templates/admin_base.html 只有一个 class="admin-sidebar"，PR10 是唯一一级 sidebar。
- donor 业务模板不得包含 <aside>、class="side" 或 .side，不能成为第二个壳。
- web/donors/groupops-v2 不得被当成完整 v2 deployable page；admin/nav.json、registry.json、donor automation.html 不得另行装配。
- Group Ops archive 没有独立 content-package editor/list template，也没有第二个 Group Ops runtime。
- donor 其他页面如果存在 data/share 按钮，前端原样保留；本轮不屏蔽、不美化。只允许 v3 后端对非本轮 route fail-closed，不能用隐藏按钮制造“已迁移”假象。

## 3. 内容包：真实存在的 surface 和明确缺口

### 没有独立 Group Ops 内容包编辑页

固定 donor 中没有 Group Ops 内容包列表或编辑模板，也没有对应 section。Group Ops active AdminController API 的 import 集合只有 Group Ops plan/member/asset/node/webhook/preview/runtime projection functions；它没有调用：

- POST /api/admin/content-packages/preview
- POST /api/admin/content-packages
- PUT /api/admin/content-packages/{package_id}

生成的 Media content-delivery client 和 donor server handler 确实存在，但它们只是 Media-owned 后端/DTO 合同，没有 Group Ops 内容包页面、list/get route 或 Group Ops save wiring。groupOpsHistory.ts 中的 content_package 只作为 v1 历史 JSON 展示，明确“不执行”；它不是编辑器，也不应被重新解释为当前可运行节点。

因此本轮不发明内容包页面、section、字段、默认值或 API adapter，不在 donor 前端上补“内容包管理”功能。若 PR02 的内容包 UI 尚未作为可打开 Journey 提供，PR06 的“内容包 + 群运营”用户闭环仍然是未完成状态。

### 现有 Group Ops UI/API 能完成什么

Group Ops 详情页的素材面是：

- 节点 JSON 使用 GroupOpsNode；
- material_plan.references 使用 image、miniprogram、attachment、group_invite 和正整数稳定 ID；
- 三个原样 picker 读取已有 images、mpLib、attach 页面数据，把 typed 引用写回节点；
- Group Ops POST /plans/{plan_id}/content/preview 只校验计划节点、群引用、成员和 typed material shape，不建立 Media content package；
- 历史节点的 content-package JSON 只能只读查看；
- groupOpsDetail 读取 plan、content preview、run-due preview、executions、webhook descriptor，执行投影不等于发送。

真实内容包的创建/校验/版本/素材资格由 PR02 Media owner 负责。donor Media 合同的实际字段是 name、content_text、enabled、refs 和 update 的 expected_version；Preview/Create/Update 在 Media UoW 中校验 refs、记录 mutation receipt，并由 Media Store 判断 canonical/eligible refs。运行时的素材 source snapshot、digest、provider-ready preparation 和冻结发生在 donor runtime 的 MaterialBoundary，而不是 Group Ops 页面。

后续 v3 只能依赖 PR02 的稳定 Media port，例如 content delivery、Group Ops material snapshot/freeze port；不能复制 Media 表、直接读 Media 表，不能把 Product、Customer 或 Audience port 伪造成内容包依赖。

## 4. donor 页面、交互和 active request graph

### 列表页 groupops.html

registry/nav 的实际入口是 groupops.html，页面显示运营计划总数、active 数、本地队列和运营成员。原样按钮/动作是查看群目录、创建计划、每行编辑、active 时暂停、其他可转换状态启用、归档，以及 draft 行删除草稿。

模板文案明确本地队列不等于已发送，页面不接受 run-due、broadcast 或 webhook。页面请求由 readAdminRows 按 page scope 发出：Group Ops plan list 以及 scope=group_ops operation members。

### 详情页 groupopsDetail.html

详情页原样包含计划名称、最多 5 位可信 staff 候选、只读群引用 textarea 和群目录 modal、有序 message/delay 节点与高级 GroupOpsNode JSON、typed material picker、content preview/issue codes、Webhook opaque reference/复制 descriptor URL，以及 execution projection 的只读 KV/events。

保存时 saveGroupOpsPlanDto 的实际顺序是：create 或读回 plan，按 revision 依次 rename、移除/增加 staff、移除/增加 group asset、移除/增加/更新 node、写 webhook descriptor，最后 content preview 和 GET readback。每个 logical mutation 带 Idempotency-Key，每次都带上次 readback 的 expected_revision。

这不是一个数据库原子“保存全部表单”请求：前端串行发出多个独立 HTTP mutation。某一步失败时，未成功的 logical key 会保留以便重试，但已经成功的前序变更不会自动回滚。因此 v3 不能把 donor 的 UI 完整保存误报为单事务闭环；需要服务端按 UoW 和 CAS 设计可解释的部分提交/重试语义，或在适配层把 Journey 标成未闭环。

### 群目录和 staff 范围

groupOpsDirectory.ts：

- 先读取 scope=group_ops operation members；
- 按选定 owner 读取 /api/admin/automation-conversion/group-ops/groups?owner_userid&limit&offset；
- “刷新此成员名下群”显式确认后 POST /groups/sync，用 Idempotency-Key；
- 选择只修改当前表单，仍需保存计划；
- 文案明确本地目录快照不证明当前企微权限或群消息送达、不触发群发，且响应没有 Provider 读取回执。

donor runtime 的目录 adapter 只读取 owner 名下群，并要求完整 provider snapshot 才能替换本地 projection；部分页不能成为删除依据。sender resolver 绑定到每个目标群的可信 active staff owner，不从 plan member 或进程默认值猜 sender。

### 历史和观察

groupops.html?history=1 由 frozen legacy.ts dispatch 到 groupOpsHistory.ts；详情为 groupopsDetail.html?history=1&id=<plan_id>。它只读取历史计划、两来源目录、计划群和历史节点，保留 NULL/原状态/source ID，并把 content package JSON 只读显示，不同步、不激活、不发送、不调用 Provider。

automation.html?broadcast_job_history=1[&history_id=N] 是 donor 的群发任务历史外层页面，归 PR07 的 owner；本轮保留对应 35-file archive evidence，但不把 automation.html 偷带进 PR06，也不把历史观察变成新建/发送/重试入口。

## 5. HTTP route / DTO 对照

以下是固定 donor 生成 client、HTTP handler 和注册表的实际合同。当前 v3 没有注册这些 route；“active UI”只表示 frozen AdminController 会调用的 primary path，“compat/后续”不应混入本轮 adapter。

下表中从 /plans、/history 开始的 path 均拼接公共前缀 /api/admin/automation-conversion/group-ops；完整公共路径在第一行和目录表中展开。

### 计划定义

| 方法 | primary path | 请求/响应 | 当前 UI |
| --- | --- | --- | --- |
| GET | /api/admin/automation-conversion/group-ops/plans | limit,offset -> GroupOpsPlanPage | 列表读 |
| POST | 同上 | {name} -> GroupOpsPlanDetail | 创建 draft |
| GET | /plans/{plan_id} | -> GroupOpsPlanDetail | 详情读/读回 |
| PATCH/PUT | /plans/{plan_id} | {expected_revision,name} -> detail | 保存改名 |
| DELETE | /plans/{plan_id} | {expected_revision} -> detail | donor handler 实际调用 Archive |
| POST | /plans/{plan_id}/activate、/pause、/archive | {expected_revision} -> detail | 启用/暂停/归档 |
| GET/POST/DELETE | /plans/{plan_id}/members[/{staff_id}] | GroupOpsMemberRequest/revision -> page/detail | 保存 staff |
| GET/POST/DELETE | /plans/{plan_id}/group-assets[/{asset_reference}] | GroupOpsGroupAssetRequest/revision -> page/detail | 保存 opaque 群 |
| GET/POST/PATCH/PUT/DELETE | /plans/{plan_id}/nodes[/{node_id}] | GroupOpsNodeRequest/revision -> page/detail | 保存有序节点 |
| GET/PUT | /plans/{plan_id}/webhook-descriptor | {expected_revision,reference?} -> descriptor/detail | 读写 opaque descriptor |

DELETE 在 donor 中映射到 Archive，而不是物理删除；前端仅在 draft 上显示删除草稿，但 server handler 本身不能仅靠 HTTP method 保证 draft 边界。v3 adapter 必须保留这个事实并在服务端 fail-closed，不能让 active/paused 计划通过错误 route 被意外归档。

### 内容预览、运行和效果

| 方法 | primary path | DTO/状态 | 当前 UI/边界 |
| --- | --- | --- | --- |
| POST | /plans/{plan_id}/content/preview | GroupOpsContentValidation：valid、issue_codes、preview_lines、node_count、group_asset_count、safety | 详情页读；不发送 |
| POST | /plans/{plan_id}/run-due/preview | GroupOpsRunDuePreview：due count、snapshot revision、blockers | 详情页读；不受理 |
| POST | /plans/{plan_id}/run-due | GroupOpsRunSummary，HTTP 202 | backend contract；当前页面不调用 |
| GET | /plans/{plan_id}/executions | GroupOpsExecutionPage | 详情页只读 projection |
| POST | /plans/executions/{execution_id}/reconcile | generation/fence/lease/evidence digest/delivery proven -> execution | 当前页面不调用 |
| POST | /api/automation/group-ops/broadcast | {plan_id} -> run summary 202 | inbound intent；不是送达 |
| POST | /api/automation/group-ops/webhooks/{webhook_key} | JSON event body -> run summary 202 | 需验签/重放保护；当前不开放 |

execution projection 必须保留 provider_execution_eligible、real_external_call_executed、provider_accepted、delivery_proven。accepted、queued、provider_accepted 都不能直接显示成送达；delivery_proven 只能由独立 provider receipt/evidence 推进。

### 目录、员工和历史

| 方法 | path | DTO |
| --- | --- | --- |
| GET/POST | /api/admin/automation-conversion/group-ops/groups[/sync] | GroupOpsDirectoryPage；sync body {owner_staff_id,limit} |
| GET/POST | /api/admin/automation-conversion/group-ops/group-picker[/sync] | 同一目录投影；compatibility surface |
| GET/POST | /api/admin/common/operation-members[/sync] | GroupOpsOperationMemberPage/sync page，scope 固定 group_ops |
| GET | /api/admin/automation-conversion/group-ops/history/plans | GroupOpsHistoryPlanPage |
| GET | /history/directory、/history/plans/{plan_id}/groups、/history/plans/{plan_id}/nodes | 历史目录/计划群/节点 page |
| GET | /api/admin/broadcast-job-history[/{history_id}] | PR07-owned BroadcastJobHistory 只读 page |
| GET | /admin/automation-conversion/group-ops/ui、/plans/{plan_id} | donor workspace page response；需要 v3 webshell adapter |

生成 client 中另有 /enable、/disable、/groups、/webhook 等旧 aliases，以及 execution-runtime compat routes。固定 AdminController 的 active Group Ops 读写使用 primary plan/member/group-assets/nodes/webhook-descriptor/preview paths；v3 不得把 aliases 和 primary path 混成第二套实现。Aliases 只保留在兼容性审计，不是新增前端入口。

## 6. DTO、校验和状态机

### 计划和内容校验

- plan status：draft、active、paused、archived；archived 是终态。
- active 需要通过内容校验；基本 blockers 是 group_asset_required、member_required、node_required、invalid_node、legacy_material_reference_unsupported、material_preparation_pending。
- node 只有 message 和 delay；message 必须有文本或 typed material，delay 只能有正整数分钟（1 到 10080）。
- material_plan.references 只能是 image、miniprogram、attachment、group_invite，ID 为正整数，总数最多 9；图片最多 3、小程序最多 1、group invite 最多 1。旧的自由文本 material_reference 只可读兼容，不能被当成 provider-ready。
- 输入结构化 JSON 遇到 customer/OneID/openid/unionid/external_userid/phone/segment/audience/campaign/recipient 或 secret/credential 字段时 fail closed。

状态转换的 donor service 语义是 draft -> active、active <-> paused、任意非 archived 状态到 archived；active 前需要 valid content。v3 domain 当前已覆盖这些纯规则，但没有持久状态表和 HTTP。

### 运行、材料和回执

donor runtime 的状态合同是：

accepted -> provider_accepted -> delivery_proven

并允许 accepted -> outcome_unknown，以及 accepted/provider_accepted -> final_failed。

outcome_unknown -> reconciled 只能通过带 generation/fence/lease 和独立 evidence 的人工/对账路径，不能换一个幂等键盲重试。delivery_proven 必须有 provider 真实调用与可验证回执，不能由本地队列推断。

材料 intent 的状态为 material_pending -> ready_to_accept -> accepted | final_failed；未准备好时继续等待或人工阻断，上传 outcome unknown 不能自动重试。run-due 以 plan revision 和确定性 execution key 快照计划，delay 累加分钟，按 group asset × message node 产生候选；重复 run 通过 durable reservation 去重。

Webhook descriptor 只返回 same-origin path、HMAC-SHA256 和固定 header 描述，不返回签名密钥、token、credential 或 provider body。public broadcast/webhook 需要注入的 ProtocolAuthenticator、签名验真、nonce/replay 保护；nil authenticator 必须 fail closed。

## 7. donor 后端闭环证据和 v3 不能照抄的部分

### donor 已实现的参考闭环

固定 donor 的实现证据可以分为以下层次：

1. internal/groupops/app/service.go：plan detail 读取、锁、expected revision/CAS、成员/群资产/node 生命周期、Webhook descriptor、content preview；写操作通过 UoW 执行。
2. internal/groupops/store/repository.go 与 runtime_repository.go：plan、operation receipt、run、execution、intent、directory refresh 的 transaction-bound Store；plan lock 使用 FOR UPDATE，receipt reserve/complete 和 run/execution 使用冲突保护。
3. migrations 00063_group_ops_local_plans.sql、00085_group_ops_runtime.sql、00098_group_ops_material_delivery.sql：owner 表、revision/state guard、不可变 snapshot、intent guard、receipt 约束；Media 的 00083_media_content_package_delivery.sql 归 Media owner。
4. runtime internal/groupops/app/runtime.go：schedule snapshot、execution key、material boundary、EER accept/queue、execution projection、outcome_unknown 和 manual reconcile。
5. store/dispatch_runtime.go、jobqueue 和 outbound：在相同 v3/UoW 语义下持久化意图/效果收据，Provider 网络调用在事务外运行；expired attempt 进入 recovery/unknown，不重放网络调用。
6. provider/dispatch.go 与 dispatch worker：把 Provider result 映射为 accepted/provider accepted/unknown/rejected；只有真实业务调用和回执才能进入相应效果字段。
7. WeCom directory source：只读完整群目录和 operation member snapshot，先验证 active local staff owner，不创建 Customer、不访问 OneID、不猜 sender。
8. Protocol auth 和 webhook：HMAC/replay、idempotency、audit/evidence seam；外部返回 accepted 不是 delivery_proven。

donor 的 operation receipt 带 payload/result digest，plan write 还通过 EventAppender 发出 group_ops.plan_updated。这提供了幂等和事务事实的参考，但不是 v3 当前已有的完整审计系统；当前 v3 只有 EventAppender seam，Terra 仍需落同一 PostgreSQL UoW 的 owner audit/outbox。

### 不得直接搬运的 cross-domain 依赖

donor runtime Store 为实现历史版本曾直接读取 external_effects 和 Media tables，donor composition 还接入 v2 auth/contact/EER/Media/provider。这些是参考实现的证据，不是 v3 允许的 import。v3 必须：

- 通过 outbound/externaleffects 稳定 port 协调外部效果，不直接读 EER 表；
- 通过 PR02 Media port 获取 canonical material、source snapshot、prepare/freeze，不直接读 Media 表；
- 通过 PR01 的 staff/operation-member port 获取本地员工，不 import Audience/Customer/OneID 的 app/store/http/provider；
- 使用 platform jobqueue，不在 Group Ops 自建 scheduler/worker/lease/retry；
- 让所有 plan state、idempotency receipt、audit/event、outbox/effect intent 在同一 PostgreSQL UoW 原子提交；
- 保持 Provider disabled，且不把 HTTP 200/202、accepted 或 local queue 当作外部成功。

## 8. 当前 v3 缺口和 PR01/PR02 依赖

### 当前分支明确缺失

当前 internal/groupops 只有 domain、app/service.go、port 和测试/README。以下均未装配：

- Group Ops-owned Store、migration、generated SQL、CAS/lock 的 PostgreSQL implementation；
- authenticated HTTP handler、OpenAPI registration、CSRF/capability route gate；
- directory/group-picker adapter、operation-member staff adapter、sender resolver；
- runtime service、schedule cursor、run/execution/intent repository；
- Media material boundary、source snapshot、preparation continuation；
- outbound/EER accept/queue、jobqueue worker、Provider adapter；
- receipt projection、outcome_unknown recovery、evidence verifier、manual reconcile；
- historical importer/read model 和真实 v3 webshell mount；
- Composition Root registration、deployment/health/lock changes。

因此 go test ./internal/groupops/... 通过只证明 pure domain/local service seam，不证明 PR06 的持久化、定时、Provider、回执或浏览器 Journey 已完成。

### PR01

PR06 依赖 PR01 已冻结或待提供的能力：

- PR10 admin_base 和唯一一级 sidebar；
- v3 Session、capability、CSRF 和 admin route registration；
- platform UoW、事务 event/outbox、幂等/审计基础；
- internal/platform/jobqueue durable task；
- outbound/externaleffects port；
- 本地 active staff/operation member port。

这些依赖不能通过 donor 的旧 legacy.ts、Audience edge、v2 auth 或 v2 Store 旁路解决。

### PR02

PR06 依赖 PR02 Media：

- 内容包 Preview/Create/Update 的 owner API/port；
- content package version、expected_version 和 mutation receipt；
- canonical/eligible material refs；
- source snapshot、digest、provider-ready preparation、freeze 及失败/unknown 语义；
- 如果本轮要提供“内容包创建/编辑”用户 Journey，必须复用 PR02 已冻结 UI/adapter；不存在时保持缺口，不能由 PR06 新造页面。

Group Ops 只保存 typed opaque material references，不能复制 media_content_packages、refs 或 Media mutation tables，也不需要 Product、Customer、OneID 或 Audience port。

## 9. Closure checklist 和验收条件

可执行门禁是 scripts/check-pr06-closure.sh。它会：

1. 固定 donor 当前 HEAD 为 6bfbe5816bb89913c70adaca87d6a486260e016e，调用 35-file SHA-256 + cmp manifest check；
2. 验证 build.mjs -> admin/main.ts、registry 的两个 Group Ops screen，以及 main.ts -> legacy.ts -> AdminController；
3. 验证 archive 模板没有第二 sidebar，v3 admin_base 只有一个 sidebar；
4. 验证 frozen Group Ops surface 没有 content-package editor/API wiring，历史 content_package 仍只是只读；
5. 验证当前 v3 Group Ops Go files 没有 Customer/Identity/Audience/Segment/Campaign 跨域 import，也没有把 donor HTTP/Store/worker/Provider 作为 prep implementation 偷带进来。

当前应得到：

- donor：PASS，35/35 byte-exact；
- frozen browser chain：PASS；
- PR10 single sidebar：PASS；
- independent Group Ops content-package editor：不存在（这是预期的明确缺口，不是漏报）；
- v3 backend closure：FAIL/未完成，原因是 HTTP/Store/runtime/outbound/Media wiring 尚未实现。

达到真正 closure 前必须完成并分别验证：

1. 在 v3 PR10 admin_base 内以 v3-owned mount/adapter 开放仅 Group Ops list/detail/directory/history；保持 donor 35 文件和 main -> legacy -> AdminController 字节不变，不开放其他 legacy page/API。
2. 实现 Group Ops owner 表、UoW、CAS/lock、幂等 receipt、audit/event/outbox 和历史只读 read model。
3. 接入 PR02 Media snapshot/freeze，明确内容包 UI 是否已有可用入口；没有就继续标记未完成。
4. 实现 run-due/broadcast/webhook 的验签、replay、durable schedule、EER/outbound、worker、Provider disabled fail-closed、receipt/evidence/reconcile；不开放前端伪发送动作。
5. 明确 donor DELETE=Archive 的服务端 draft 边界和多请求保存的部分提交/重试语义。
6. 对 active Journey 做 browser/API replay：创建、编辑、复制式重读、staff、group directory、nodes/material preview、activate/pause/archive、Webhook descriptor、execution projection、unknown/reconcile；证明没有 Customer/OneID/audience/recipient 依赖。

本提交不 push、merge 或 deploy；只提交审计文档和门禁脚本。
