# 九板块开发验收矩阵

更新时间：2026-09-05。只记录本轮证据；部署、生产 Provider 验收不计入本轮完成。

## 审核与集成交付状态

- 总控在每个 PR 可审时立即审核，不等待全部板块完成；CI 通过与总控审核分别记录。
- 审核通过的准确提交持续进入 `codex/v3-parity-integration`，总集成 PR 以 main 为 base 持续运行组合 CI。
- 个体 PR 不逐个合并 main。总集成 PR 冻结唯一发布候选后，才提交用户做最终生产上线确认；确认后一次合并、一次部署。
- 本矩阵后续为每个板块同时记录“个体审核结果”和“已纳入集成 HEAD”。未进入集成或联合验收未通过的项目不得标记整体完成。

当前未合并总集成 PR：[PR #141](https://github.com/qianlan33333-png/AI-CRM-v3/pull/141)。已纳入的实现来源为 #133、#135、#136、#138；#140 的总控文档也已纳入本地集成提交，待下一次推送运行组合 CI。#137、#139 与 #142 当前均未获最终纳入批准。

| 来源 | 来源 HEAD / 独立 CI | 纳入提交 | 组合 CI | 当前结论 |
|---|---|---|---|---|
| #133 客户同步 | c3195e225be67907c14ff3057c6561eeb17138f1 / 33947397405 SUCCESS，deploy SKIPPED | 28a5a8d0ccc4ad9f17e11e7e1c2a8d6e4a95b6b6 | 后续组合持续验证 | 已纳入；联合缺口仍开 |
| #135 渠道欢迎语 | f23dc40177472c609a175fcda641fe7d25a1528c / 33948768346 SUCCESS，deploy SKIPPED | 9a37a50734ff8124dae50e3edace16cc9a48f7e8 | 后续组合持续验证 | 已纳入；01/09 联合缺口仍开 |
| #136 商品与支付 | 1329c1e13e0c7c6630599ef81e9998250cdbf773 / 33949608992 SUCCESS，deploy SKIPPED | 650a95a6f045f74351d9b8b5e4cea76f1c29edc0 | 33954344813 因后续提交取消 | 已纳入；03/04 联合缺口仍开 |
| CI 必需 rg | c85c4fc2d91c241689a5ec89fd14d37405ea7f54 / 总控源码审核通过 | c85c4fc2d91c241689a5ec89fd14d37405ea7f54 | 33954398244 因纳入 #138 取消；33954780090 SUCCESS且实际执行rg安装，deploy SKIPPED | 已纳入并通过当前组合 |
| #138 周期权益与优惠券 | 599b5bf1102b66476db4c9bc10d5f6b049c5dfcd / 33954299348 SUCCESS，deploy SKIPPED | 6eab2bd347fccd748211531a3967b9159cdb0f4e | 33954780090 SUCCESS，deploy SKIPPED | 独立领域已纳入；整体未完成 |
| #140 总控文档 | f9e1633519a59c49857dac565916a98a42fae17a / 33954387627 SUCCESS，deploy SKIPPED | 733cdd44d9431136c652bdb2bdd53c7fa640b9e6 | 待下一次推送 | 已批准纳入 |

| 板块 | PRD | 开发状态 | PR / HEAD | 旧行为/前端 | PostgreSQL/恢复 | 身份/效果协议 | 总控审核 |
|---|---|---|---|---|---|---|---|
| 客户同步 | 01 | #133 已纳入；恢复缺陷 #142 等待实际PG/race | [#133](https://github.com/qianlan33333-png/AI-CRM-v3/pull/133) / c3195e2；[#142](https://github.com/qianlan33333-png/AI-CRM-v3/pull/142) / 5242e17 | Host 列表测试通过 | #133 PostgreSQL 16 通过；#142 多员工多页真实River CI运行中 | #142限定HTTP 200/-1三次上限，其他临时失败保持12次 | #133已纳入；#142源码通过，待CI；01/08联合待补 |
| 问卷 | 02 | 增量修复继续；真实运行时消费尚未通过 | [#134](https://github.com/qianlan33333-png/AI-CRM-v3/pull/134) / b26ea89；[#137](https://github.com/qianlan33333-png/AI-CRM-v3/pull/137) / c835d95 | 已改真实HttpApi原页测试；参数/渠道/跳转回读待最终审核 | 实际PG接纳和同键重放已推进通过；River消费超时继续定位 | 0075注册kind；快照时间精度已修 | 未批准；父PR绿灯不证明整板块闭环 |
| 商品与支付 | 03 | 独立缺陷PR开发测试通过；联合接线待完成 | [#136](https://github.com/qianlan33333-png/AI-CRM-v3/pull/136) / 1329c1e | 商品码新链接、数字历史别名、自购确认测试通过 | CI33949608992 PG16/race通过，含NULL约束反例 | 新会话受益人未确定；旧会话仅精确重放 | 独立PR通过；03/04闭环未完成 |
| 周期权益与优惠券 | 04 | 独立领域PR通过并已纳入；03/04联合接线待完成 | [#138](https://github.com/qianlan33333-png/AI-CRM-v3/pull/138) / 599b5bf | 券数据页与周期读Port已实现；03联合挂载中 | CI33954299348 PG16/race通过，含不同开通/退款顺序与历史共存 | 仅保留独立历史覆盖；券跨时刻重放通过 | 独立领域已批准并纳入6eab2bd；整体未完成 |
| 自动化运营 | 05 | 已有局部实现；为即时集成保留名额暂缓 | 本地 faa7149；未PR | 未验收 | PG/运行时未验收 | Owner只读Port已开发；composition未交付 | 不算完成；原上下文待继续 |
| AI 助手 | 06 | 待派发 | — | 未验收 | 未验收 | 未验收 | 待审 |
| 群运营 | 07 | 待派发 | — | 未验收 | 未验收 | 未验收 | 待审 |
| 渠道欢迎语 | 08 | 独立实现开发测试通过；组合验收待进行 | [#135](https://github.com/qianlan33333-png/AI-CRM-v3/pull/135) / f23dc40 | 回调、管理、素材和原入客回归通过 | CI33948768346 PG16真实River拥堵/重启及race通过 | 过期原因、零期限禁止发送及schema readiness通过 | 独立PR审核通过；仍需01/09组合回归 |
| 会话存档 | 09 | 当前CI绿；员工筛选缺陷待修 | [#139](https://github.com/qianlan33333-png/AI-CRM-v3/pull/139) / 3362619，含PR135 | 独立Host和Access Port已实现；员工参与者关联查询仍错误 | CI33953398123 PG原子/并发与Linux SDK实际加载检查通过 | 回调重放/导入继续位置及事实对账已修 | 未批准；补真实PG员工筛选及重复员工去重 |

每项证据必须说明命令、环境、结果、对应提交；已有 CI 不能证明不同 HEAD。测试跳过、配置 disabled 与不可验证项目单独列出。只有适用测试实际通过、PR 可审查且总控审核通过，才标记完成。

## 联合测试

- 03+04：购买→券预占→支付确认→券核销→权益开通/续期→退款调整；乱序、并发和 unknown。
- 01+08：客户尚不存在时 welcome 独立及时执行，普通流程仍幂等建客。
- 08+09：同回调入口按 Event 严格分发，存档处理不延误欢迎语。
- 05+06+07：共用 outbound/素材/效果内核，跨业务幂等范围不碰撞。

## 派发记录

| 任务 | 执行智能体 | 模型 | 工作区/分支 | 状态 |
|---|---|---|---|---|
| 01 | /root/customer_sync | Terra high | /Users/qianlan/Downloads/新CRM/.codex-worktrees/customer-sync / codex/customer-sync-parity | 已交回；PR133 CI通过，总控保留板块联合验收 |
| 02 | /root/survey | Terra high | /Users/qianlan/Downloads/新CRM/.survey-prd02-worktree / codex/survey-prd02-test-push | 父PR134 CI通过；当前在b26ea89上堆叠补旧合成测试按钮及完整板块证据 |
| 08 | /root/channel_welcome | Terra xhigh | /Users/qianlan/Downloads/新CRM/.codex-worktrees/channel-welcome-prd08 / codex/channel-welcome-20s | 已交回，当前HEAD实际PG/River及race通过；09已基于它堆叠 |
| 03 | /root/product_payment | Terra xhigh | /Users/qianlan/Downloads/新CRM/.codex-worktrees/product-payment-prd03 / codex/product-payment-parity | PR136修复SQL CHECK的NULL穿透后已交回；联合04尚未接线 |
| 04 | /root/entitlement_coupon | Terra xhigh | /Users/qianlan/Downloads/新CRM/.codex-worktrees/entitlement-coupon-prd04 / codex/entitlement-coupon-parity | 已启动旧规则对照与领域实现；与03协调券事务契约 |
| 09 | /root/message_archive | Terra xhigh | /Users/qianlan/Downloads/新CRM/.codex-worktrees/message-archive-prd09 / codex/message-archive-parity | 基于08准确提交堆叠，已启动通知/SDK/事务实现 |
| 05 | /root/automation | Terra high | /Users/qianlan/Downloads/新CRM/.codex-worktrees/automation-prd05 / codex/automation-prd05-parity | 保留局部工作暂缓；02或03交回名额后恢复 |
| 集成 | /root/integration | Sol medium | /Users/qianlan/Downloads/新CRM/.codex-worktrees/v3-parity-integration / codex/v3-parity-integration | 持续纳入已审核准确HEAD并创建总PR；保持开发与集成并行 |

当前调度：02与03/04联合任务持续开发；05已保留原上下文和工作树并暂缓。总集成任务 /root/integration（Sol medium）已启动，立即纳入已审133/135/136并建立总集成PR，再并行补联合验收。根继续审核137/138/139；CI绿与业务审核分别记录。全程最多三个活动执行智能体。

已有对话派发受应用审批策略阻止，未送达；本表不把它们标记为已接单。本任务内的开发智能体已实际启动。

## 已批准的实现协调

- 08 使用 migration 0066，新增 Channel-owned channel_welcome_intents，保存首次时间/20秒期限、grant引用、配置/素材快照、effect绑定与未发送原因；不是新队列。KindChannelWelcome 改投共享 River 的 outbound_welcome 队列，仍复用既有 EER 执行内核。
- 08 提议并获准的回调窄接口：wecom.DecryptedCallbackEvent{CorpID, CallbackKey, Plaintext, ReceivedAt} 与 DecryptedEventDispatcher.DispatchDecryptedEvent(context.Context, DecryptedCallbackEvent) error；最终符号以交付代码为准。09 经该内部已验签边界接入，不放宽普通外部联系人必填字段。
- 02 已核实 CompletionIntentAccepter 仅声明而未实际接线；批准补 SubmissionService 同事务接受、OwnerOutbound/KindSurveyCompletion、outbound router 及 completion 回写，预计复用0018表无需迁移。必须实现真实可配置连接器并以本地 HTTP Provider 验证，不只注册 fake adapter。
- 02/08 对共享 effect kind/router 各自只添加自己的分支；callback 接纳由08独占。
- 01 已核实分页、恢复、同事务和成功后 stale 已存在；本次补 Provider disabled/权限/429/5xx/非法响应的安全分类与持久业务状态，不改 callback、composition/OpenAPI，不需迁移。
- 02 进一步对照供体后确认旧版外推元数据需要持久化，分配 migration 0067（Survey-owned 配置兼容元数据和受保护不可变外推快照）。不改变0066归属。旧签名协议、字段和指定 kind/scope 的可信身份必须兼容，不以新造的 customer 字符串替代旧外部用户身份。
- 03 分配 migration0068：payment session 的未确定受益人及明确选择来源；保留旧值，修复未指定即默认付款人的接线，不新增赠送产品。
- 04 预留 migration0069、0070；coupon/权益 Owner 各自持有新增业务表，03不读写券表。
- 09 预留 migration0071、0072；复用08的已验签事件边界，消息域独占消息、游标、媒体引用及导入收据。
- 02 增量预留migration0073，存Survey-owned合成测试外推快照；真实提交与合成测试使用明确来源分支，不伪造Customer或答卷。
- 02 分配migration0074，Survey回执持久化Sink收到的安全执行事实和尝试序号；历史未采集值保持未知，不能用状态或记录次数推断Provider响应。
- 02申请并分配migration0075，由External Effects Owner扩展真实schema的outbound/survey_completion允许组合；保留所有原kind和0066欢迎语队列合同。此前真实PG接纳受旧CHECK拒绝，不能只改测试约束。
- 03/04联合接线分配0076，Order Owner冻结订单checkout金额、券引用、产品类型与周期天数，历史未知不猜测补写；0077归Coupon Owner保存稳定唯一不可变public_slug，旧券通过可对账兼容规则取得分享入口。不得用数值ID冒充slug。

## 总控保留的验收项

- 01：PR133 对错误分类的修复已有真实PG证据；基线的单员工单页journey不能代表多员工多页、跨页相同客户关系及恢复后的实际继续执行。集成验证需补该场景，并复核 HTTP200 业务暂时错误（如 Provider busy）的可恢复分类，防止只按HTTP状态分类。
- 02：旧参数页面必须在没有任何回执时仍可编辑，保存带现有CSRF、保留当前开关/配置引用且可重开回填。键值参数不要求用户编辑JSON。静态语法检查和已有UI staging不代替新用户流程测试。
- 02：总控继续对照发现旧版外推测试按钮是实际合成请求能力，当前路由仍固定写 disabled 回执。已在 PRD02 第8节明确补齐要求；还需整份PRD的管理/发布/OAuth/历史归属等现有能力证据，不把单个外推缺陷PR当作全部验收。
- 02：外推Provider测试当前仅主请求/默认disabled，签名校验复用了被测签名函数。补充独立接收方固定向量、重定向零泄漏、未知不重发、旧配置变动不改变已接受快照以及实际Runtime恢复证据；不得把实现镜像断言当协议一致性测试。
- 08：真实共享River运行中隔离普通队列拥堵；重启后必须实际执行或明确过期，不能仅重新打开Repository证明queued记录存在。
- 06：总控预审已定位机器输入身份自升 verified、认证nonce混入业务幂等摘要、未解析目标逐项反馈及旧入口映射，详见PRD06第7节；尚未派发实现。
- 05：已冻结供体六种实际模板及核心语义。d6实际只装配ActiveContacts且误用目录更新时间，详见PRD05第7节；尚未派发实现。
- CI 环境曾出现 `rg: command not found`，若相关脚本在条件表达式中吞掉错误，绿色状态不代表这些边界检查已运行。最终集成任务需补齐CI必需工具或给出同等真实检查证据，不放宽门禁。
- 当前CI仅接受base为main的PR。依赖分支仍从已确认前置HEAD开发，创建PR时以main为base，并在正文列出前置PR及增量比较范围，才能触发隔离PG检查；这不授权合并、main push或手动发布工作流。
- 02：PR137 d0a710f的CI33951136109在供体哈希阶段失败（web/src/admin/controller.ts被改）。不得更新哈希或放宽白名单；恢复冻结文件，用户流程适配迁入V3 Host再重新检查。该轮尚未执行PG测试。
- 03/04：PR138正式提供OrderCouponCoordinator、ServicePeriodEntitlementCoordinator、HistoricalServicePeriodSourceCoordinator与ServicePeriodPublicReader。联合任务还需独立周期公开页/支付入口、订单金额周期券快照、同UoW核销及权益结算、couponData/spProductData挂载、周期会员读取和历史逐条对账；独立Port不计完整能力。
