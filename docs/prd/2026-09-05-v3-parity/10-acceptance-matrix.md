# 九板块开发验收矩阵

更新时间：2026-09-06。只记录本轮证据；部署、生产 Provider 验收不计入本轮完成。

## 审核与集成交付状态

- 总控在每个 PR 可审时立即审核，不等待全部板块完成；CI 通过与总控审核分别记录。
- 审核通过的准确提交持续进入 `codex/v3-parity-integration`，总集成 PR 以 main 为 base 持续运行组合 CI。
- 个体 PR 不逐个合并 main。总集成 PR 冻结唯一发布候选后，才提交用户做最终生产上线确认；确认后一次合并、一次部署。
- 本矩阵后续为每个板块同时记录“个体审核结果”和“已纳入集成 HEAD”。未进入集成或联合验收未通过的项目不得标记整体完成。

当前未合并总集成 PR：[PR #141](https://github.com/qianlan33333-png/AI-CRM-v3/pull/141)。最新已推组合 `597115462e789866621ddd6992643264ac52c7cd` 的CI33982372650失败，仍为两个旧客户同步/欢迎语fixture漏载0086；修正PR161准确03713dc本身又被PR152旧历史回读竞态挡住。PR152新b0c369增加等待真实AI完成投影，已通过CI33982622908（PG16/race，deploy跳过）并经源码审核纳入16e09ba5b07740fae3be2203703e13ef879d484c，组合继续验证。上一完全通过组合 `6ca8dc0e87a498761679dbbbb3df32dde257535e` / CI33978425968是真实PG16、race、SDK ABI及仓库检查成功，deploy跳过。

已审纳入的最新业务源为PR143 `e9e62809a569be09771e6107039e64c934201e37` / CI33981662058（完整历史目标及部分中断恢复），PR152 `950164b00d8a19e41fde605f507fec4746223232` / CI33980221396，PR160 `4982e4fb05c5e5c1b00990d463a42dd1248e8ccf` / CI33980816276。对应合并提交分别b0b20281、1bcb8fcb、7b8a1dc3，均保留准确来源祖先。PR158原后台UI/中文测评键、PR159自动化历史逐条对账、PR162的05/06/07共同运行及上述fixture修正仍待准确CI与审核，尚未冻结最终发布候选。

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
| #134/#137 问卷外推 | 807873cba38bd180859e303a32082539f402a51d（保留父b26ea890ddcdcb7998f6114b959fc2f9c59881d6）/ 33955879884 SUCCESS，deploy SKIPPED | 1c8ddb65efcf66d545bb7817e54b91bba62a0e54 | 33956447855 因纳入#145取消 | 已纳入；历史#146已纳入且3edc92c组合通过 |
| #146 问卷历史导入 | 76a6d3054600af0026099187cd3a56a8c58e27fa / 33958659330 SUCCESS，deploy SKIPPED | f1bc29f32ec7f261793ba7c7e8998cf046f43b30 | 33959472998 SUCCESS，deploy SKIPPED | 已审核纳入且组合通过；逐条事实对账及固定快照故障用例实际PG16通过 |
| #145 客户同步/欢迎语组合 | e2a3c41d1846bda44d4e5966e480f6eb8bce56a9 / 33956133831 SUCCESS，deploy SKIPPED | d74187fec1953939aa10ca1dbbc33a12c39a3c51 | 33956614448 SUCCESS，deploy SKIPPED | 已纳入；同身份根、双员工关系与欢迎发送标识稳定通过 |
| #147 欢迎语/存档组合 | 44187711305cef21e9d2c0338bde3322bdb45bf7 / 33956596446 SUCCESS，deploy SKIPPED | 205cf1a5a44c6f7cc6ab1e8e12c25c598b801f7f | 本轮文档提交后组合CI | 已批准纳入；真实PG16中存档Provider阻塞不延误欢迎语 |
| #149 会话存档客户入口 | 94fad015a57424f159f5360ef835c62a6e69d6db / 33957048733 SUCCESS，deploy SKIPPED | 148e891a9c8b3a91cb6a612afed166ceafb47f5e | 本轮组合CI | 已批准纳入；复用现有客户查找并由canonical CustomerID进入独立Host |
| #150 会话存档runner打包 | 4652583c62e93400fd163ea1cc4b23c753782fae / 33957899828 SUCCESS，deploy SKIPPED | e9a54cb2d64fcf548c9257d7aefdd4ed39e83d69 | 本次推送后组合CI | 源码及准确HEAD检查通过；Linux amd64 CGO=1构建与真实官方SDK ABI共用入口；已纳入 |
| #148 群写叶子、节点意图及送达读取 | 10b40e08aa9b8fc8844a5edd0f35d24a05c6438e / 33961391856 SUCCESS，deploy SKIPPED | 4e9b836a320e671bfbaa78b59e66d0516acc0c53 | 33962020352 SUCCESS，deploy SKIPPED | 本批源码审核与PG16/race通过；实际River顺序、全UI与历史仍待下一批 |
| #151 AI助手旧调用方与整单执行 | 890444cf29070a6e0a18a757aeda7e54e28ac57e / 33961381169 SUCCESS，deploy SKIPPED | 61b802d3ad4d5d113b93864b978a4c04f97b1ceb | 33962020352 SUCCESS，deploy SKIPPED | 可信身份、签名HTTP、整单审批、River恢复、附件Host与旧素材映射导入已审核；05/06/07组合仍待 |
| #153 群运行时/旧页/历史导入 | 629e38489d76903b0dd21d6614941e4f2cffeb69 / 33968755244 SUCCESS，deploy SKIPPED；保留014fd7b与4e51b676 | 889144c63426dfd7ead4ca827694037fa4dd94e5 | c2cf0ef / 33969480007 SUCCESS，deploy SKIPPED | 真实River、暂停恢复、多计划、逐行历史导入PG/HTTP通过；同一导入事实的历史浏览器Host补齐中 |
| #143 商品、购买、会员表与历史联合 | e9e62809a569be09771e6107039e64c934201e37 / 33981662058 SUCCESS，deploy SKIPPED；保留270d8f98/06ecac29 | b0b202818f8ec7a3ab81d3756ffe062d594d5764 | 本批组合待验证；既有26ec5cf已绿 | 实际券/权益全字段与源映射/隔离目标核验、schema2提取与v1摘要兼容、部分导入中断恢复/并发互斥/重放/对账通过 |
| #157 群历史导入后原Host阅读 | c8792192a3462b1953b06acd12260a3d95cb918a / 33974411097 SUCCESS，deploy SKIPPED | d1adbccda6c114fc271662dcb4a056672545369d | f4d6948 / 33975204629 SUCCESS，deploy SKIPPED | 真实PG导入节点经实际HTTP/Host展示；合成分页反例验证迟到响应/ID碰撞/XSS，未把合成后页冒称生产历史 |
| #158 问卷OAuth与提交结果联合 | 039c823e9677986c1180b3ec121430aeb779b418 / 33977653567 SUCCESS，deploy SKIPPED | 21a563e224b0d2195dd204a31321c6f6a228c507 | 6ca8dc0 / 33978425968 SUCCESS，deploy SKIPPED | 0090前向修正与真实PG readiness；实际OAuth Adapter/OneID/并发提交/结果授权/CSV/历史版本通过，Admin鉴权为测试Port，原后台UI继续 |
| #154 HXC共用事实 | a093751b15ee2b51b349e01fa42590baf2eb5422 / 33969310749 SUCCESS，deploy SKIPPED | 25a711209840ae5826518237b152ba26da07512f | f4d6948 / 33975204629 SUCCESS，deploy SKIPPED | 真实源EXPLAIN、同源会员、PG固定代与清理并发通过；已放行03/05稳定Port接入 |
| #155 群真实Access、unknown与AI同库运行时 | 3c69142ea55a5a40362b5d28042aa64dc5fb5546 / 33969767286 SUCCESS，deploy SKIPPED；保留0130e88 | 4242d30730b6736ff67838bfd05d6432aef6f341 | 4ec08bb / 33970748481 SUCCESS，deploy SKIPPED；父批79d26e1通过 | 真实Access/unknown及AI和Group共同PG/River/Outbound通过；自动化参与的最终组合仍待 |
| #152 自动化六来源、原表单、人工待审与固定素材运行时 | b0c3694c9a85bbfe5de05574d90188df9f83fc94 / 33982622908 SUCCESS，deploy SKIPPED；保留950164b及其CI33980221396 | 16e09ba5b07740fae3be2203703e13ef879d484c | 本批推送后验证 | 原页面JSDOM→实际Runtime HTTP/PG→AI待审/整单审批/回执；自动混合素材/冻结漂移/unknown/重启与分页50+通过；历史及最终共用运行仍待 |
| #160 存档历史导入工具随包交付 | 4982e4fb05c5e5c1b00990d463a42dd1248e8ccf / 33980816276 SUCCESS，deploy SKIPPED | 7b8a1dc37bcdbefed98536ef453ae2ff684671fe | 本批推送后验证 | 既有release构建加入archive importer；真实合成installer缺commerce/archive binary均拒绝，不改变workflow触发与部署权限 |

| 板块 | PRD | 开发状态 | PR / HEAD | 旧行为/前端 | PostgreSQL/恢复 | 身份/效果协议 | 总控审核 |
|---|---|---|---|---|---|---|---|
| 客户同步 | 01 | #133与恢复缺陷#142、01/08组合#145均已纳入 | [#133](https://github.com/qianlan33333-png/AI-CRM-v3/pull/133) / c3195e2；[#142](https://github.com/qianlan33333-png/AI-CRM-v3/pull/142) / 5242e17；[#145](https://github.com/qianlan33333-png/AI-CRM-v3/pull/145) / e2a3c41 | Host列表通过；welcome先执行且发送标识不变 | #142真实PG16恢复；#145真实Inbox→同根双关系→River重启通过 | HTTP 200/-1限定三次，其他临时失败保留12次预算 | 独立恢复及01/08组合通过；整体随最终装配复核 |
| 问卷 | 02 | 领域/外推/历史及OAuth联合已纳入，原后台UI增量继续 | #137 / 807873c；#146 / 76a6d30；#158 / 039c823e | 冻结外推配置/日志及公开模式已有证据，管理操作专项继续 | 039c823e CI真实PG16/race，0090前后Readiness与OAuth→submit/result/CSV通过 | 真实OneID同根、同键并发重放/漂移、匿名结果拒绝、修改后历史答案保留 | 0090与本批审核完成，仍待原后台UI及最终组合 |
| 商品与支付 | 03 | #143完整历史目标与中断恢复已审核 | #143 / e9e62809 | 冻结周期公开页、会员表保存视图/协作/分享、全量字段组合与购买恢复测试通过 | CI33973228872真实PG16/race；支付/退款签名及历史apply/replay/reconcile通过 | 未知建单保留原键；支付/券核销/权益故障同UoW回滚 | 03/04独立闭环及历史完整对账通过；最新组合待验证 |
| 周期权益与优惠券 | 04 | #138独立领域及#143联合实现已纳入 | #138 / 599b5bf；#143 / e9e62809 | 周期公开购买、券领取与数据页、原会员表接线通过 | 实际签名支付核销与开通、并发退款一次撤回、超额退款冲突、历史终态零效果通过 | 付款人与受益人边界、券占用/释放/核销、原单恢复各有专项证据 | 03/04及联盟批次审核通过，最终组合与证据核账保留 |
| 自动化运营 | 05 | #152运行时批次已审并纳入 | #152 / 950164b | 六Owner来源、原模板/人工群发详情页实际HTTP/PG通过 | 共享River自动混合素材、固定快照漂移拒绝、unknown不重发、AI跨页计数通过 | 人工确认同UoW仅创建AI待审计划，整单审批后发送；测试鉴权Port单列 | 独立批次通过；159历史对账、05/06/07共用运行和最新组合待验收 |
| AI 助手 | 06 | #151独立板块增量已纳入且7b5b4f7组合通过 | [#151](https://github.com/qianlan33333-png/AI-CRM-v3/pull/151) / 890444cf29070a6e0a18a757aeda7e54e28ac57e | 冻结单人/批量调用方；素材回读/编辑/审阅Host守恒通过 | 真实Identity/AI Store与签名HTTP→River→实际本地WeCom叶子通过；unknown原键保留 | 0080导入合法缺映射及目标漂移反例通过；intake不自升可信、不建客 | 已批准纳入61b802d；05/06/07联合及最终装配保留 |
| 群运营 | 07 | 主流程、历史、旧Host、真实撤权/unknown及AI共用运行时已纳入 | #153 / 629e384；#155 / 3c69142；#157 / c879219 | 原主页面、历史标题/正文/附件与分页安全通过 | PG16/River两群顺序/重启/暂停恢复/历史导入及实际Host通过 | Access撤权、unknown不重发、AI+Group共享运行时通过 | 板块专项已通过，最新组合与05参与最终联合保留 |
| 渠道欢迎语 | 08 | 独立实现、01/08组合#145及08/09组合#147已纳入 | [#135](https://github.com/qianlan33333-png/AI-CRM-v3/pull/135) / f23dc40；[#145](https://github.com/qianlan33333-png/AI-CRM-v3/pull/145) / e2a3c41；[#147](https://github.com/qianlan33333-png/AI-CRM-v3/pull/147) / 4418771 | 回调、管理、素材和原入客回归通过 | 独立拥堵/重启、01/08同根及08/09阻塞隔离通过 | 过期原因、零期限禁止发送及schema readiness通过 | 三项已批准纳入；整体随最终装配复核 |
| 会话存档 | 09 | #139/#144/#147/#149/#150均已审核纳入 | [#139](https://github.com/qianlan33333-png/AI-CRM-v3/pull/139) / 3362619；[#144](https://github.com/qianlan33333-png/AI-CRM-v3/pull/144) / 7d558bd；[#147](https://github.com/qianlan33333-png/AI-CRM-v3/pull/147) / 4418771；[#149](https://github.com/qianlan33333-png/AI-CRM-v3/pull/149) / 94fad01；[#150](https://github.com/qianlan33333-png/AI-CRM-v3/pull/150) / 4652583 | #149已复用现有客户搜索结果进入独立归档Host | 员工筛选/1001分批及#147 PG16阻塞隔离通过 | 回调重放/导入对账已修；#150保持默认disabled并补Linux amd64 runner | 独立实现与联合验收已通过；最终组合与生产验收分开记录 |

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
| 02 | /root/survey，后续定向修复交回根审核 | Terra high | survey-history-repair / codex/survey-prd02-history-import | #137外推已纳入；#146@76a6d30实际PG16 CI通过并纳入f1bc29f |
| 08 | /root/channel_welcome | Terra xhigh | /Users/qianlan/Downloads/新CRM/.codex-worktrees/channel-welcome-prd08 / codex/channel-welcome-20s | 已交回，当前HEAD实际PG/River及race通过；09已基于它堆叠 |
| 03 | /root/product_payment | Terra xhigh | /Users/qianlan/Downloads/新CRM/.codex-worktrees/product-payment-prd03 / codex/product-payment-parity | PR136已纳入；03/04联合任务保留未批准本地checkpoint，当前未占活动名额 |
| 04 | /root/entitlement_coupon | Terra xhigh | /Users/qianlan/Downloads/新CRM/.codex-worktrees/entitlement-coupon-prd04 / codex/entitlement-coupon-parity | PR138独立领域已纳入；03/04联合闭环仍待恢复 |
| 07 | /root/group_receipts接手原clone | Terra high | /Users/qianlan/Downloads/新CRM/.codex-worktrees/group-ops-prd07 / codex/group-ops-parity | #148@299617b后按§9修送达读取与冻结素材；其后完整River/UI/history仍待交付 |
| 09 | /root/message_archive | Terra xhigh | /Users/qianlan/Downloads/新CRM/.codex-worktrees/message-archive-prd09 / codex/message-archive-parity | 领域实现、员工筛选、联合隔离及客户入口已由#139/#144/#147/#149纳入；#150发布runner待审 |
| 05 | /root/automation | Terra high | /Users/qianlan/Downloads/新CRM/.codex-worktrees/automation-prd05 / codex/automation-prd05-parity | 保留局部工作暂缓；02或03交回名额后恢复 |
| 集成 | 根审核与纳入；Sol原任务已交接 | 根负责指挥审核 | /Users/qianlan/Downloads/新CRM/.codex-worktrees/v3-parity-integration / codex/v3-parity-integration | #141滚动纳入准确HEAD，保持开发并行，未合并main |

当前调度：03会员视图批次member_grid（Terra xhigh）、07运行时与原UI批次group_runtime_ui（Terra high）、05六Owner来源与原表单批次automation_sources（Terra high）开发并行。06独立批次已批准，待05/06/07联合验收。根接手准确HEAD审核与总分支纳入，集成Sol任务已交接释放名额，必要集成实现后续再派。全程最多三个活动开发智能体。

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
- 06：总控预审已定位机器输入身份自升 verified、认证nonce混入业务幂等摘要、未解析目标逐项反馈及旧入口映射，详见PRD06第7节；已派发并持续按后续证据修正，具体状态以上方最新板块行优先。
- 05：已冻结供体六种实际模板及核心语义。d6实际只装配ActiveContacts且误用目录更新时间，详见PRD05第7节；已派发并持续按后续证据修正，具体状态以上方最新板块行优先。
- CI 已闭环记录：组合CI从c85c4fc起显式安装并验证`rg`，缺失会失败；后续绿色组合均实际执行相关门禁。
- 当前CI仅接受base为main的PR。依赖分支仍从已确认前置HEAD开发，创建PR时以main为base，并在正文列出前置PR及增量比较范围，才能触发隔离PG检查；这不授权合并、main push或手动发布工作流。
- 02 历史记录：PR137早期d0a710f因冻结供体漂移失败；最终807873c恢复冻结文件并通过CI33955879884后已纳入。历史#146@76a6d30随后完成逐条对账并通过实际PG16检查，已纳入。
- 03/04：PR138正式提供OrderCouponCoordinator、ServicePeriodEntitlementCoordinator、HistoricalServicePeriodSourceCoordinator与ServicePeriodPublicReader。联合任务还需独立周期公开页/支付入口、订单金额周期券快照、同UoW核销及权益结算、couponData/spProductData挂载、周期会员读取和历史逐条对账；独立Port不计完整能力。

## 最新协调记录

- 0078 GroupOps业务意图及任务收据语义按PRD07§8批准；0079 Product会员表保存视图/协作/分享元数据按PRD03§9批准。0080随后分配Media旧素材受验证映射，供06通过稳定Port消费；0081分配GroupOps修复未配置Webhook多计划创建，0082分配GroupOps既有历史导入Port收据，0083分配Segment旧四种刷新模式，0084分配HXC共用原字段投影，0085归Segment增量/日刷新事实，0086归WeCom主负责人，0087预留Automation人工待审关联，0088分配Order原联盟字段，0089归Outbound原自动意图内容快照，0090归Survey OAuth重定向约束前向修正，0091分配Survey原测评业务键约束前向兼容，下一空闲编号0092。
- 06首次派发前PRD已完成，严格复用可信Identity Reader、现有机器鉴权与审批/效果内核。

## 2026-09-05 后续批次复核

- #143：0079会员表现已嵌入冻结旧模板、JS/CSS及图标，脱离仓库目录仍可发布。准确HEAD a55a372076e9749b5707a8003764217d5eb59ef6继续CI；实际整页操作证据、周期公开页Python模板转换缺陷、支付恢复、03/04签名闭环与历史对账尚未完成，不纳入该HEAD。
- #152：六Owner来源代码与Host转换已创建PR；WeCom/Channel/Order/HXC已有新增实际PG测试，Survey/Radar及真实表单组合证据继续补齐。审核定位本地StaffID与数字Provider userid无类型比较风险，要求Owner显式类型及碰撞反例；修复未闭环前不纳入。
- #153：群共享River顺序与重启开始实际CI，延迟持久化断言暴露Provider调用发生与Completion/Continuation提交的时间差。继续按业务落盘事实验证。旧记录DTO不接受provider_accepted同时delivery_proven=true，须在非冻结Host投影适配并走完整旧页面验证。
- 这些均为旧能力上线必要缺陷与验收补齐，没有引入新的效率优化项目或生产操作。

- #153@4e51b676已通过实际CI33963458294并由总控审核纳入056f8243；验收按初始execution/EER/River job同事务落盘、后继intent/execution/effect/job同一关系链与未到期事实判断，EER真实状态为queued，不由HTTP调用数推定提交。Provider已接纳与送达读取分开，HTTP投影保留runtime_state。后续a29及新UI/异常证据尚未批准，不包含在本次纳入。

- 0081已按PRD07§13批准，PR153@a3ca91224008b84da92abfb20c1c4c0d7118d8eb同时补暂停恢复及多计划创建，CI33964390855验证中；未批准纳入。PR143@220672eca36187f460c94e7085c91d7df284186a会员表本批已提交，公开购买与完整事实缺口仍待。PR152@856c4219009b95b0d0146fba1124ce6513cf8475六来源真实Owner PG在CI33964237463验证中，旧表单Host继续开发。

- PR152@856c421实际CI33964237463 SUCCESS、deploy SKIPPED，但新Host源码审核发现基础保存被拦截、慢加载双渲染竞争、日期格式与预览语义缺口；暂不批准，不以CI代替旧页面行为验收。PR153后续守卫夹具修正到4323ed40fac09b70f50149c10e4656804c08213e，CI33964599150验证中。

- PR143@220672e CI33964351360的PG/仓库检查通过，但新真实HTTP浏览器fixture出现并发数据竞争，待修；分组跨页计数另有明确源码修正要求。PR152@6d5fb519e65731fb7965bcd6cbe226573a57826a已修表单只读预览与基础保存，尚待全链路复审；原引用解析、刷新模式及Radar首次Owner/HXC状态仍未完成。

- 0084 HXC共用字段合同已形成；总控实际只读核实源MySQL schema，原字段存在，尚未实现或刷新生产。PR153安装测试fixture修到014fd7b13b8960b36df0445ebe8215caad4dc58c / CI33965315786；PR143公开渲染与分组分页修复后继续付款恢复，尚未批准。

- PR143@1b148814e47ce0740a41a4f9cda4217366cb264f审核阻断：丢创建响应再换OAuth会话可能重复建单，且笼统409清除恢复键；已要求按PRD03§16修复，未纳入集成。CI33965527717另发现Product测试跨领域import Payment HTTP，须移至Composition Root。

- PR153准确HEAD `014fd7b13b8960b36df0445ebe8215caad4dc58c` 已通过CI33965315786（实际PG16、race和原UI，deploy SKIPPED），总控审核通过并纳入 `a96d5a5392de17e03e37d3435cae3da22e2d8723`。覆盖暂停后恢复、计划/素材变化守卫、0081未配置Webhook多计划创建；资格变化使用可变Access Port夹具，不冒称真实Access数据库撤权。组合发布清单冲突仅机械合并，完整本地安装契约通过。0082历史导入610d133及新HXC共享事实均独立待审。

- #141准确组合4e17909bb5b71208836947ff24c0e17476a48ecc通过CI33966346271（完整PG16、race、官方SDK ABI及仓库检查，deploy SKIPPED）。#153历史当前a2cb58d待CI；#143付款恢复和Order续费事实55ccee7待CI/全量审核；#152刷新语义和主负责人来源仍开；#154 HXC首批源码与真实源检查已退回。当前四个clone分别由三个智能体独占推进，根仅文档/审核/机械集成。


- 本轮新增审核：#153@144abcc常规真实PG通过，但CI33967600701在race第二遍因测试使用固定legacy_history_source schema失败，已退回独占测试schema修复；未纳入。#154@2846c79真实源只读EXPLAIN通过，但CI33967755271缺测试必填枚举，选源SQL还有NULL与同源期限语义问题，仍未批准。#152@87e4978 CI33967526099日刷新SQL bigint/text失败，后续修复待审。#143@bf34840补原Order字段全量关系查询，尚待准确HEAD检查。#155@0130e88补真实Access撤权及River首节点unknown，独立联合验收待审。0085归Segment、0086归WeCom，下一空闲0087。


- PR155准确HEAD0130e88eaeea4b6a930f27d951a61936f83d09f2通过CI33967837266（PG16、race、官方SDK ABI、冻结UI，deploy SKIPPED），源码审核通过并作为merge parent纳入28149072e70175244fdf7bb5ceb4f521e0cf8e2b。实际Access is_active撤权在Provider调用前阻止执行，真实River/本地WeCom接收后断开响应形成原EER unknown且无后继执行；AI+Group同库组合仍待，不以本子批代替05/06/07全验收。


- PR153历史准确HEAD629e38489d76903b0dd21d6614941e4f2cffeb69通过CI33968755244（PG16、race、冻结UI，deploy SKIPPED），源码审核通过并作为merge parent纳入889144c63426dfd7ead4ca827694037fa4dd94e5。包含受保护五来源提取、密封快照、逐行导入/重放/隔离/漂移对账、标题正文附件真实PG与HTTP回读、nullable旧员工引用及中文字符长度；只写GroupOps历史Owner，不创建当前计划/客户/效果。独立history浏览器操作和最终同库装配继续回归。

- PR154@a093751已批准并纳入25a7112；安装测试清单冲突仅机械保留0068/0078/0081/0082/0084，完整本地安装契约通过。HXC运行代码仍使用既有OneID与MySQL只读刷新，无新同步器；源查询沿b131的真实只读EXPLAIN通过，未执行生产刷新/应用0084。03/05允许按该准确HEAD接入。

- PR155准确HEAD3c69142ea55a5a40362b5d28042aa64dc5fb5546通过CI33969767286（真实PG16、race、冻结UI、SDK ABI，deploy SKIPPED），源码审核通过并作为merge parent纳入4242d30730b6736ff67838bfd05d6432aef6f341。实际签名AI intake、整单审批与两群执行在同库共享River/EER及实际本地WeCom协议叶子；初始三项效果及两条延时后继持久化，重放及重启不增发、不创建新效果。05自动化参与的最终共同接线仍保留。

- 最终流程复核新增明确旧能力缺口：02按PRD02§11补冻结后台动作及OAuth/提交/结果真实PG+HTTP；05按§18复用已有AI待审计划恢复人工群发，0087仅预留必要运行关联，不另造审批引擎。六来源注册语义还需核对真实Owner来源，HXC已发布用户行的Registered=true不能代替所有注册条件。

- PR143准确06ecac2已源码复核并在独立CI33973228872中实际通过PG16及race，作为merge parent纳入df2dfd42。冲突仅保留存档与商品各自装配、两类公开入口、既有SDK CI及迁移清单并集；本机相关包和安装契约通过，PG缺环境跳过，不冒称联合数据库已在本机运行。资金旅程本地签名回调/真Owner事务不是生产支付；OAuth身份恢复和历史导入分别由其专项证明，未把所有测试称同一次真实Provider流程。

- PR157准确c879219已通过CI33974411097（真实PG16、race、SDK ABI及冻结UI）并审核纳入d1adbccda。实际PG source节点走封存/导入/HTTP/Host原只读渲染；第二页为明确合成的浏览器竞态反例，保留真实plan身份以检验迟到响应与碰撞字段，未声称这些是实际导入历史。未修改冻结业务前端，V3 Host桥只展示无新发送命令。

- 2026-09-06：PR143准确270d8f98通过CI33975881872并经总控源码审核，作为merge parent纳入f2bfd585。0088区分联盟未知/确认空值，旧无字段快照摘要保持不变，真实PG验证隔离行与目标篡改、CAS和Outbox失败回滚；原Host实际编辑/清空通过。安装清单冲突仅机械取各板块并集。问卷真实OAuth发现0018重定向约束双反斜杠缺陷，按PRD02分配0090前向修复；0089继续为Outbound独占，不能按旧分支文件推断空闲。

- 2026-09-06：26ec5cf准确组合已通过CI33977313172，check SUCCESS、deploy SKIPPED。03/04核账复核仍有既有sidebar历史目标状态/时间字段对账缺口，按PRD03§21继续，不能只凭0088联盟切片标全历史通过。自动化历史目录已交member_grid独立clone修复；automation_sources继续运行时/素材/UI，group_runtime_ui继续问卷，最多三个开发智能体。

- 2026-09-06：PR158准确039c823e通过CI33977653567，源码审核后纳入21a563e；两个安装检查冲突只保留0088与0090并集。新纵向测试使用实际Survey/Identity/Customer PG和实际OAuth Adapter，仅微信HTTPS交换重定向到严格allowlist本地接收方；Admin安全为测试Port。两个公开模式均实际提交并授权读取，同一可信UnionID只一根；不能称生产OAuth验收或完整后台点击旅程。

- 2026-09-06：PR152950与已审151重叠的素材入口机械合并，保留严格legacy映射、HTTPFacade附件可用性及新冻结摘要/发送前Owner校验；补新fixture既有Identity/Staff接口参数，删除自动合并产生的相同重复常量/上传函数，未新建身份或效果机制。Go目标包测试/编译、安装契约和真实合成安装器本机通过，PG本机因无DSN跳过，组合PG/race仍交CI。
- PR1435f恢复设计因applying永久阻断退回，078/e9修复及第二条失败后的恢复待准确CI/复审；PR1594e7补全目标/隔离核验，但刷新mode保存及同definition不同配置仍待修，均未纳入。原后台UI最新测试只修原问卷ID定位和实际异步完成等待，不放宽Owner成功条件。
- automation_sources已按12末尾合同从1bcb8fc独立承担最终05/06/07共用PG/River/EER运行；member_grid同时推进143恢复和159历史，group_runtime_ui继续158原页面。保持三个开发名额，根只文档、审核和机械集成。

- 组合141a4fd本机make check退出0（PG因无DSN跳过），实际CI33981762102暴露旧fixture漏0086：CustomerSyncRiverRestart与CustomerSyncAndWelcome都停在reconciling，不能以本机通过掩盖。group_runtime_ui按12共同接线合同从该准确HEAD独立修复fixture，无业务/新迁移/超时放宽。

- PR143 e9e62809真实PG16/race CI33981662058通过并根审纳入b0b2028：已持久第一条目标/收据后第二条失败，再运行首条replay及后续import守恒；隔离恢复后重新置applied并重新reconcile，同run互斥，无新效果。源码从270到e9的全部增量均已审，已完成的143历史不再计为待开发。

- 2026-09-06：PR152 b0c369完整CI33982622908与源码审查通过，纳入16e09ba5。原测试提前读取历史存在时序竞争，现等待真实recipient/binding完成回执及实际RuntimeService只读投影，保留原历史断言。#159/#161/#162依赖此准确修正并行验证，未以重跑旧HEAD代替修正。
