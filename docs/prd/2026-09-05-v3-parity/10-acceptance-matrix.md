# 九板块开发验收矩阵

更新时间：2026-09-05。只记录本轮证据；部署、生产 Provider 验收不计入本轮完成。

## 审核与集成交付状态

- 总控在每个 PR 可审时立即审核，不等待全部板块完成；CI 通过与总控审核分别记录。
- 审核通过的准确提交持续进入 `codex/v3-parity-integration`，总集成 PR 以 main 为 base 持续运行组合 CI。
- 个体 PR 不逐个合并 main。总集成 PR 冻结唯一发布候选后，才提交用户做最终生产上线确认；确认后一次合并、一次部署。
- 本矩阵后续为每个板块同时记录“个体审核结果”和“已纳入集成 HEAD”。未进入集成或联合验收未通过的项目不得标记整体完成。

当前未合并总集成 PR：[PR #141](https://github.com/qianlan33333-png/AI-CRM-v3/pull/141)。已纳入的实现来源为 #133、#135、#136、#137、#138、#139、#142、#144、#145、#147、#149；#140 的总控文档也已纳入。当前集成 HEAD `d700b3d714ef5b724831cc7c2198057a56de968b` 的 CI33957966530 正在运行。#150 Linux cgo runner打包按审核意见更新后正在CI；#146 的02历史对账也在新提交上重新检查，均未批准。

| 来源 | 来源 HEAD / 独立 CI | 纳入提交 | 组合 CI | 当前结论 |
|---|---|---|---|---|
| #133 客户同步 | c3195e225be67907c14ff3057c6561eeb17138f1 / 33947397405 SUCCESS，deploy SKIPPED | 28a5a8d0ccc4ad9f17e11e7e1c2a8d6e4a95b6b6 | #145及后续组合持续验证 | 已纳入；01/08联合缺口已由#145闭环 |
| #135 渠道欢迎语 | f23dc40177472c609a175fcda641fe7d25a1528c / 33948768346 SUCCESS，deploy SKIPPED | 9a37a50734ff8124dae50e3edace16cc9a48f7e8 | #145/#147及后续组合持续验证 | 已纳入；01/08与08/09联合缺口已闭环 |
| #136 商品与支付 | 1329c1e13e0c7c6630599ef81e9998250cdbf773 / 33949608992 SUCCESS，deploy SKIPPED | 650a95a6f045f74351d9b8b5e4cea76f1c29edc0 | 33954344813 因后续提交取消 | 已纳入；03/04 联合缺口仍开 |
| CI 必需 rg | c85c4fc2d91c241689a5ec89fd14d37405ea7f54 / 总控源码审核通过 | c85c4fc2d91c241689a5ec89fd14d37405ea7f54 | 33954398244 因纳入 #138 取消；33954780090 SUCCESS且实际执行rg安装，deploy SKIPPED | 已纳入并通过当前组合 |
| #138 周期权益与优惠券 | 599b5bf1102b66476db4c9bc10d5f6b049c5dfcd / 33954299348 SUCCESS，deploy SKIPPED | 6eab2bd347fccd748211531a3967b9159cdb0f4e | 33954780090 SUCCESS，deploy SKIPPED | 独立领域已纳入；整体未完成 |
| #140 总控文档 | f9e1633519a59c49857dac565916a98a42fae17a / 33954387627 SUCCESS，deploy SKIPPED | 733cdd44d9431136c652bdb2bdd53c7fa640b9e6 | 33955272229 因纳入 #142 取消 | 已批准纳入 |
| #142 客户同步恢复 | 5242e17930776df3e5b9cdb4e3c632b199658ba4 / 33954830233 SUCCESS，deploy SKIPPED | 4fe92c109f129a06d83ed460c87107b593e14f74 | 33955384604 SUCCESS，deploy SKIPPED | 已纳入；01独立恢复项及后续01/08组合通过 |
| #139/#144 会话存档与员工读取 | 7d558bd7cf7ba74f4188cbb68a34ffd8a24ecc61（保留父336261941f58810108328bf841c042c19dca4c18）/ 33955835930 SUCCESS，deploy SKIPPED | f41a2b13fcde01a9f614454ac983285461ae17b2 | 33956346715 因后续集成提交取消 | 已纳入；入口由#149、08/09组合由#147闭环 |
| #134/#137 问卷外推 | 807873cba38bd180859e303a32082539f402a51d（保留父b26ea890ddcdcb7998f6114b959fc2f9c59881d6）/ 33955879884 SUCCESS，deploy SKIPPED | 1c8ddb65efcf66d545bb7817e54b91bba62a0e54 | 33956447855 因纳入#145取消 | 已纳入；02历史#146仍未通过 |
| #145 客户同步/欢迎语组合 | e2a3c41d1846bda44d4e5966e480f6eb8bce56a9 / 33956133831 SUCCESS，deploy SKIPPED | d74187fec1953939aa10ca1dbbc33a12c39a3c51 | 33956614448 SUCCESS，deploy SKIPPED | 已纳入；同身份根、双员工关系与欢迎发送标识稳定通过 |
| #147 欢迎语/存档组合 | 44187711305cef21e9d2c0338bde3322bdb45bf7 / 33956596446 SUCCESS，deploy SKIPPED | 205cf1a5a44c6f7cc6ab1e8e12c25c598b801f7f | 本轮文档提交后组合CI | 已批准纳入；真实PG16中存档Provider阻塞不延误欢迎语 |
| #149 会话存档客户入口 | 94fad015a57424f159f5360ef835c62a6e69d6db / 33957048733 SUCCESS，deploy SKIPPED | 148e891a9c8b3a91cb6a612afed166ceafb47f5e | 本轮组合CI | 已批准纳入；复用现有客户查找并由canonical CustomerID进入独立Host |
| #150 会话存档runner打包 | 4652583c62e93400fd163ea1cc4b23c753782fae / 33957899828 运行中 | 待总控批准后纳入 | 待纳入后组合CI | Linux amd64专用CGO=1构建；保留macOS应用交叉构建；源码与CI待批准 |

| 板块 | PRD | 开发状态 | PR / HEAD | 旧行为/前端 | PostgreSQL/恢复 | 身份/效果协议 | 总控审核 |
|---|---|---|---|---|---|---|---|
| 客户同步 | 01 | #133与恢复缺陷#142、01/08组合#145均已纳入 | [#133](https://github.com/qianlan33333-png/AI-CRM-v3/pull/133) / c3195e2；[#142](https://github.com/qianlan33333-png/AI-CRM-v3/pull/142) / 5242e17；[#145](https://github.com/qianlan33333-png/AI-CRM-v3/pull/145) / e2a3c41 | Host列表通过；welcome先执行且发送标识不变 | #142真实PG16恢复；#145真实Inbox→同根双关系→River重启通过 | HTTP 200/-1限定三次，其他临时失败保留12次预算 | 独立恢复及01/08组合通过；整体随最终装配复核 |
| 问卷 | 02 | 独立外推运行时已批准纳入；历史导入验证为另一个未完成任务 | [#134](https://github.com/qianlan33333-png/AI-CRM-v3/pull/134) / b26ea89；[#137](https://github.com/qianlan33333-png/AI-CRM-v3/pull/137) / 807873c；[#146](https://github.com/qianlan33333-png/AI-CRM-v3/pull/146) / 98f0c67 | 冻结Host QR当前/全局两分支恢复并通过 | 外推CI33955879884通过；#146此前真实PG answer target fact drift已修；测试夹具摘要保持合法32字节后由CI33958099242重跑 | 0075注册kind；不可变快照/回执事实已补 | 外推增量已批准纳入；历史#146新提交未审核，板块整体未完成 |
| 商品与支付 | 03 | 独立缺陷PR开发测试通过；联合接线待完成 | [#136](https://github.com/qianlan33333-png/AI-CRM-v3/pull/136) / 1329c1e | 商品码新链接、数字历史别名、自购确认测试通过 | CI33949608992 PG16/race通过，含NULL约束反例 | 新会话受益人未确定；旧会话仅精确重放 | 独立PR通过；03/04闭环未完成 |
| 周期权益与优惠券 | 04 | 独立领域PR通过并已纳入；03/04联合接线待完成 | [#138](https://github.com/qianlan33333-png/AI-CRM-v3/pull/138) / 599b5bf | 券数据页与周期读Port已实现；03联合挂载中 | CI33954299348 PG16/race通过，含不同开通/退款顺序与历史共存 | 仅保留独立历史覆盖；券跨时刻重放通过 | 独立领域已批准并纳入6eab2bd；整体未完成 |
| 自动化运营 | 05 | 已有局部实现；为即时集成保留名额暂缓 | 本地 faa7149；未PR | 未验收 | PG/运行时未验收 | Owner只读Port已开发；composition未交付 | 不算完成；原上下文待继续 |
| AI 助手 | 06 | 待派发 | — | 未验收 | 未验收 | 未验收 | 待审 |
| 群运营 | 07 | #148已实现企微群写/只读目录协议叶子；按PRD §8继续真实装配、顺序与回执 | [#148](https://github.com/qianlan33333-png/AI-CRM-v3/pull/148) / 315ddca | 供体协议测试及CI33957206809通过；原UI/最终配置仍待装配 | 共享River即时/延时/重启顺序、暂停/取消竞态及历史逐条对账未验收 | 当前composition仍not-configured/disabled；0078回执与后续节点衔接待实现 | 独立协议叶子未批准纳入；板块继续开发 |
| 渠道欢迎语 | 08 | 独立实现、01/08组合#145及08/09组合#147已纳入 | [#135](https://github.com/qianlan33333-png/AI-CRM-v3/pull/135) / f23dc40；[#145](https://github.com/qianlan33333-png/AI-CRM-v3/pull/145) / e2a3c41；[#147](https://github.com/qianlan33333-png/AI-CRM-v3/pull/147) / 4418771 | 回调、管理、素材和原入客回归通过 | 独立拥堵/重启、01/08同根及08/09阻塞隔离通过 | 过期原因、零期限禁止发送及schema readiness通过 | 三项已批准纳入；整体随最终装配复核 |
| 会话存档 | 09 | #139/#144/#147/#149已纳入；发布runner#150按审核意见更新后CI中 | [#139](https://github.com/qianlan33333-png/AI-CRM-v3/pull/139) / 3362619；[#144](https://github.com/qianlan33333-png/AI-CRM-v3/pull/144) / 7d558bd；[#147](https://github.com/qianlan33333-png/AI-CRM-v3/pull/147) / 4418771；[#149](https://github.com/qianlan33333-png/AI-CRM-v3/pull/149) / 94fad01；[#150](https://github.com/qianlan33333-png/AI-CRM-v3/pull/150) / 4652583 | #149已复用现有客户搜索结果进入独立归档Host | 员工筛选/1001分批及#147 PG16阻塞隔离通过 | 回调重放/导入对账已修；#150保持默认disabled并补Linux amd64 runner | #147/#149已纳入；#150仍待批准，整体未完成 |

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
| 02 | /root/survey | Terra high | /Users/qianlan/Downloads/新CRM/.survey-prd02-worktree / codex/survey-prd02-history-import | #137外推已纳入；当前#146@98f0c67补历史逐条目标事实/密文/错误归属与隔离事实，CI33958099242等待执行 |
| 08 | /root/channel_welcome | Terra xhigh | /Users/qianlan/Downloads/新CRM/.codex-worktrees/channel-welcome-prd08 / codex/channel-welcome-20s | 已交回，当前HEAD实际PG/River及race通过；09已基于它堆叠 |
| 03 | /root/product_payment | Terra xhigh | /Users/qianlan/Downloads/新CRM/.codex-worktrees/product-payment-prd03 / codex/product-payment-parity | PR136已纳入；03/04联合任务保留未批准本地checkpoint，当前未占活动名额 |
| 04 | /root/entitlement_coupon | Terra xhigh | /Users/qianlan/Downloads/新CRM/.codex-worktrees/entitlement-coupon-prd04 / codex/entitlement-coupon-parity | PR138独立领域已纳入；03/04联合闭环仍待恢复 |
| 07 | /root/group_ops | Terra high | /Users/qianlan/Downloads/新CRM/.codex-worktrees/group-ops-prd07 / codex/group-ops-parity | #148@315ddca协议叶子CI通过；按§8继续0078回执、共享River后续节点及真实composition |
| 09 | /root/message_archive | Terra xhigh | /Users/qianlan/Downloads/新CRM/.codex-worktrees/message-archive-prd09 / codex/message-archive-parity | 领域实现、员工筛选、联合隔离及客户入口已由#139/#144/#147/#149纳入；#150发布runner待审 |
| 05 | /root/automation | Terra high | /Users/qianlan/Downloads/新CRM/.codex-worktrees/automation-prd05 / codex/automation-prd05-parity | 保留局部工作暂缓；02或03交回名额后恢复 |
| 集成 | /root/integration | Sol medium | /Users/qianlan/Downloads/新CRM/.codex-worktrees/v3-parity-integration / codex/v3-parity-integration | 持续纳入已审核准确HEAD并创建总PR；保持开发与集成并行 |

当前调度：02在#146补历史导入隔离事实，07在#148按§8继续真实装配，集成任务持续纳入已审准确HEAD。03/04联合任务保留本地checkpoint，05/06等待活动名额恢复。CI绿色与业务审核分别记录，全程最多三个活动执行智能体。

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

- 01 已闭环记录：#142补多员工多页、跨页同客、River重启继续与HTTP200/-1繁忙有界恢复；#145补欢迎先执行后的同根双关系，均已批准纳入。
- 02：旧参数页面必须在没有任何回执时仍可编辑，保存带现有CSRF、保留当前开关/配置引用且可重开回填。键值参数不要求用户编辑JSON。静态语法检查和已有UI staging不代替新用户流程测试。
- 02：总控继续对照发现旧版外推测试按钮是实际合成请求能力，当前路由仍固定写 disabled 回执。已在 PRD02 第8节明确补齐要求；还需整份PRD的管理/发布/OAuth/历史归属等现有能力证据，不把单个外推缺陷PR当作全部验收。
- 02：外推Provider测试当前仅主请求/默认disabled，签名校验复用了被测签名函数。补充独立接收方固定向量、重定向零泄漏、未知不重发、旧配置变动不改变已接受快照以及实际Runtime恢复证据；不得把实现镜像断言当协议一致性测试。
- 08 已闭环记录：#135完成真实共享River拥堵/重启与20秒边界，#145完成01/08同客，#147完成存档Provider阻塞下欢迎语实际执行，均已批准纳入。
- 06：总控预审已定位机器输入身份自升 verified、认证nonce混入业务幂等摘要、未解析目标逐项反馈及旧入口映射，详见PRD06第7节；尚未派发实现。
- 05：已冻结供体六种实际模板及核心语义。d6实际只装配ActiveContacts且误用目录更新时间，详见PRD05第7节；尚未派发实现。
- CI 已闭环记录：组合CI从c85c4fc起显式安装并验证`rg`，缺失会失败；后续绿色组合均实际执行相关门禁。
- 当前CI仅接受base为main的PR。依赖分支仍从已确认前置HEAD开发，创建PR时以main为base，并在正文列出前置PR及增量比较范围，才能触发隔离PG检查；这不授权合并、main push或手动发布工作流。
- 02 历史记录：PR137早期d0a710f因冻结供体漂移失败；最终807873c恢复冻结文件并通过CI33955879884后已纳入。当前未完成项是#146历史导入对账。
- 03/04：PR138正式提供OrderCouponCoordinator、ServicePeriodEntitlementCoordinator、HistoricalServicePeriodSourceCoordinator与ServicePeriodPublicReader。联合任务还需独立周期公开页/支付入口、订单金额周期券快照、同UoW核销及权益结算、couponData/spProductData挂载、周期会员读取和历史逐条对账；独立Port不计完整能力。
