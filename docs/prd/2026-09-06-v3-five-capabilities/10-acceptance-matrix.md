# 五项验收与审核矩阵

基线50b86c8，更新2026-09-06。只按证据更新，不把计划/派发当完成。

| 板块 | PRD | 当前实现证据 | 完整板块 PR / 最新已知 HEAD | 根审核与剩余项 |
|---|---|---|---|---|
| 负责人迁移 | 01 已批准 | 旧流程/真实PG/恢复/协议/历史专项通过，Linux Chrome收口中 | #171 `96b79b8b2182a93b9ce0f52b82d4dbb01f9bef8a` | 9ee88d1实际失败为excel_scope_select动态JS语法；新HEAD加入所有动态表达式编译预检及真实文件导入，待该HEAD完整Linux CI，不标为通过 |
| 通用客户标签 | 02 已批准 | 根PG/race、历史/101恢复/协议、真实Chromium与全CI通过 | #169 `6f4b63c53c000cbc1c523ece78a9fec37147b735` 已合并 | main8ec5072于13:01 UTC再次核实线上ready；代码已部署，13:23 UTC通用标签开关已启用并核实API/Worker实际加载；真实业务验收未进行 |
| 配置中心运行生效 | 05 已批准 | 根PG/race、实际消费者/历史/制品、真实Chrome及CI通过 | #170 `07b0af371db7a97620924a34ee5975a74c775d39` 已合并 | 已随5494537及后续main8ec上线；生产业务配置发布与真实消费者业务验收另记 |
| 商品／订单外推 | 04 已批准 | paid完整链路、历史CLI、协议已根PG/race验证，真实Chrome收口中 | #172 `0fd897ea5b84482055338673850134cab21cb377` | 0fd修复缺构建制品导致503；Linux CI34035483933继续发现browser configuration save did not finish，已派定点修复；未批准整板块 |
| 通用开放平台 | 03及03a 已批准 | 冻结56条method/path，逐个接现有领域Port | #173 远端`ba0155a16d366889573697a12dbd05e50e12d776`，本地`d7e9ba28402191bed1a2d9e5dfb0722d87c10c74` | 4接通/4部分/48待迁移；根发现旧存档raw_payload wrapper与新extract不兼容，已退回修复并要求真实旧样例；Segment机器actor、Survey历史投影及其余路由继续 |
| 新前端壳 | 12 已批准 | 沿用原PR164工作，在独立clone合入main8ec | #164 远端`c8819d7a9b837c00116682e860310717e5f94b20`，本地a96b162 | 现有远端CI在donor manifest失败；待修正确V3适配归属、完整制品、模块Host路由、侧边栏CSP/素材可见性，最终组合浏览器和部署待验收 |

共用验收：完整路由/Composition、冻结供体复用、PG原子性/并发/重启、身份权限、未知结果、历史零新效果。各PR需链接实际日志/测试/浏览器证据，跳过与Mock明确标识。

## 独立交付与合并记录

按用户最新要求，整板块验收通过后独立合并；不再以总集成 PR 为交付单位。#168 停止接收业务 HEAD，已关闭并保留阶段记录。它此前只纳入文档及共用修复，没有五项业务实现需撤回。

- 共用修复 #167，准确 HEAD `cbac1f6bdb2b868985c577ba0cff11482430a19e`：完整 CI、根独立真实 PostgreSQL race 测试与审核通过。已独立 squash 合并 main：`53c1c62e7db7924b979aa11fd8345d969fadf4ec`，既有自动部署已成功，生产readyz核对到53c1c62；不代表五板块生产业务验收。
- 配置中心 #170、客户标签 #169 整板块代码验收已完成并独立合并；其余三项仍未完成，单项证据通过不填为整板块完成。
- 每个板块合并前记录最终准确 HEAD、真实 PG/浏览器/协议/历史证据、CI及 review 结论；合并与既有部署流水线结果另外记录。
- 配置发布、凭据启用、真实转接/打标/外推、生产历史导入均未执行，不能由合并状态推导。

历史导入、真实消费者、浏览器操作、Provider协议及整体CI必须分别验收；局部通过不覆盖待办。

## 配置发布根审核证据

#170 业务提交f8fcc95与历史提交71f3bab已合入共享cbac1f6；根在独立review worktree对b1a8b5运行真实PG测试：Config Store/HTTP/Module/Webshell、历史CLIapply/replay/verify、cmd/aicrm自动化Provider全旅程race、旧任务config-observed数据库约束反例，均通过且无PG跳过。随后35ca888仅安装制品登记、ccc39cb仅README密钥输入说明；根对最终ccc39cb独立运行安装契约/缺失0094阻断测试通过。

消费者范围明确仅 `automation.operations.max_recipients_per_run`；旧键未确认等价的一律只读excluded/no_v3_runtime_equivalence，不声称已激活旧配置。生产配置发布与真实Provider仍未进行。

标签#169修正至74f560e：根独立PG/race通过。剩余网关无可信拒绝证据时unknown分类、Channel忙碌标签不回滚入客、只读观察刷新及历史导入由执行任务继续，尚未批准整板块。

2026-09-06 最终门禁补充：根独立运行 #170 c2906bb 的配置定义/运行配置历史边界正例及两项负例脚本通过；db70fcb仅合并已审核main基线。#171 deef2f2已补101行分段中止/重启测试，20k实际规模、历史与完整旅程仍待最终证据。

浏览器证据澄清：JSDOM可证明DOM与实际HTTP/PG交互，不等于真实Chromium页面。所有板块最终浏览器项须另有真实页面/Host资源/操作链路证据；原有JSDOM用例继续保留。新历史CLI必须纳入既有release制品清单，部署不自动执行历史导入。

根审核新增：负责人已恢复供体文件字节冻结，并独立通过严格转接协议测试；实际Provider仍逐客户调用，与旧100客户一批不符，未准予整板块完成。标签历史CLI 5fdf180已交初稿，根发现actor与企微follow员工混淆、真实源导出未闭合，已要求修正；观察并发后续修正453ce60待根复核。所有新迁移仍未部署。

根复核补充（2026-09-06 09:30 UTC）：标签55369d82af616ab6f3ba669740e4a96da7838182在独立PG中串行包执行历史CLI、WeCom和Customer Store全量race测试均通过，无跳过。最初多包并行共库在旧0005 public trigger函数DDL发生tuple concurrently updated；串行复测排除了该测试环境竞争，不作为业务缺陷。历史源实际提取、actor/follow员工区分、来源漂移和重叠快照幂等已覆盖。

独立复核：负责人1c6dd1767ec9f63602c10dec253872a7e1b4eea5历史源SQL实表fixture与导入/重放/核验真实PG race通过；捕获shell的负例env拼写使测试未触达scope校验，已要求修正。标签361f2e0e7cc2c6c6139d221c364d877e1c2e422c对账等待刷新后保留最新观察的真实PG race专测通过。

部署只读快照：#167 main流水线仍处于制品分块上传；生产/opt/aicrm/current仍为ed8b6d333051300bff473bc3e3bd6d68e9dfe9a2。仅发现该53c1c62上传目录存在分块，尚未确认该版本安装，未重复上传或执行安装。

## 第一个独立板块交付：配置中心 #170

2026-09-06 09:43:39 UTC，根对准确HEAD `07b0af371db7a97620924a34ee5975a74c775d39` 审核通过并独立squash合并，main commit `5494537fb4d95a916c2754a3c1387335905d2861`。CI `34024687693` / job `101463455790` 全绿，实际cmd/aicrm Chrome旅程随全量及race执行，覆盖首次登录直达Host、发布2→发布3→选择旧版本回滚为新revision且有效值2。另有根独立PG/消费者/历史与安装门禁证据；PR正文已重写为完整最终交付。未向#168集成，也未等其余四项。main自动部署结果、生产配置发布与历史实际导入继续单列。

标签后续：发现早先agent推送使用本地origin；根已通过github远端推送a85并核实真实PR HEAD，执行者后续fee4025同样通过GitHub核实。现在Chrome真实页面操作包含手动刷新结果，不用CDP独立fetch成功冒充页面显示。T04原101用例仅验证持久入库，尚未实际runtime停止/重建；已派补测，不能据此提前验收。

部署事实更新：#167 的 main CI34022234700/deploy于2026-09-06 09:42:37 UTC成功，根随后读生产 `/readyz` 确认 release_sha=53c1c62e7db7924b979aa11fd8345d969fadf4ec/status=ready。配置中心5494537的main流水线34025458810已启动，尚未确认部署。标签fee4025 CI失败定位为测试文件直接os.Getenv违反平台配置读取边界，已要求修正，不能以Chrome尚未运行写成浏览器验收通过。

负责人94e895a7d1768fa57d48ada17994afd7139861f2根独立PG/race通过：101人真实River两批、冻结身份摘要、过期Owner版本拒绝、unknown artifact保留明确成功行CAS及重放、严格叶子partial101协议；历史capture正负scope脚本通过。db6470a并入5494537后CI发现组合测试在outbound import wecom/adapter违反跨域边界，已要求原测试移cmd/aicrm，不能放宽gate或删协议断言。

## 历史修正快照（2026-09-06 10:10 UTC；最终状态以上表及下文最新记录为准）

- #171 f711900：架构跨域测试已修。CI34026096663在真实Chrome失败 `local_only preview was not persisted`。另需修正测试企业scope不一致、LinuxCI启动失败不能skip、第二次导航不能使用旧DOM、两模式不能只验accepted。页面目前手填scope/数字员工ID，尚未符合旧员工选择与范围操作复用要求。已通过的94e895a真实101/River/部分结果等证据不重复处理。
- #169 fee4025：当前CI阻断是浏览器测试直接读环境；执行者正在修复真实路由/ProviderRouter/UoW及来源开关矩阵，并补实际101恢复，需新HEAD根复核。
- #172 c0ca7ec：CI34026106666失败 `web/v3/productAdapter.ts:242:59 Object.hasOwn`，保留原TS目标做适配。旧paid完整组装协议修正进行中，禁止发明按商品类型分支的paid形状。
- #170 main5494537：main检查已通过，既有部署流水线进行中；未核验新版readyz，未发布生产业务配置。

| 板块 | 实现 | 测试 | 审核 | 合并 | 部署 | 真实业务验收 |
|---|---|---|---|---|---|---|
| 负责人 | 已有实现，旧页面及旅程待修 | 局部PG通过，当前Chrome失败 | 未通过整板块 | 未合并 | 未部署 | 未进行 |
| 标签 | 已有实现，真实接线/来源控制待复核 | 局部PG通过，最终专项/CI待交 | 未通过整板块 | 未合并 | 未部署 | 未进行 |
| 配置 | 代码范围完成 | 真实PG/Chrome/CI通过 | 通过准确07b0af3 | #170已合并 | main既有部署进行中 | 未进行 |
| 商品外推 | #172开发中 | paid真实PG已有，编译修正及完整验收待交 | 未通过整板块 | 未合并 | 未部署 | 未进行 |
| 开放平台 | 独立执行者开发中 | 未提交完整证据 | 未开始最终审核 | 未合并 | 未部署 | 未进行 |

根复核更新（2026-09-06 10:25 UTC）：标签rebase后的编译/Provider/Completion装配遗漏均已具体定位，97b1ea835b370817f535736dfb781c158b5851cc在根独立PG/race通过真实101人停/重建与两向来源门禁，日志 aicrm-five-tag-97b-root-review.log。执行者继续最终路由/浏览器门禁后提交GitHub新HEAD；不能把本地HEAD当已推送。商品687f4cc根独立PG/race支付完整旅程和checkout响应丢失/会话重放通过；CI34026945723失败在商品Host两条DOM断言（452通过/2失败），另有document关闭后observer错误。TS2550已修，不重复派。开放平台#173首checkpoint已建；CI34027083142准确失败为openplatform/http跨域import access/app，根同时提出请求read scope不能继承client全部write权限、可信proxy链须防伪造前缀的具体授权修正。均未整板块批准。

标签7ce9869的后续路由/迁移装配根独立PG/race验证通过：CustomerTagCommandCompositionHTTP、CustomerSyncJourney、ChannelWelcomeAcceptance，日志 aicrm-five-tag-7ce-root-review.log；没有因新增通用标签把同步/欢迎语路径破坏。7ce9869已核实推到GitHub。MCP真实旧scope/audience模板与默认停用/轮换停用规则见03新增节，取代开发checkpoint引入的新mcp词汇。

## 最新交付状态（2026-09-06 10:58 UTC）

客户标签 #169 准确 HEAD `6f4b63c53c000cbc1c523ece78a9fec37147b735` 已通过根审核，完整 CI 34028056519/job 101472459611 全绿。Linux 专门 Chromium 步骤设置 AICRM_REQUIRE_CHROMIUM_JOURNEY=1，实际登录、两客户加/去标签、确认和手动刷新完成；DOM 验证目录映射名称，PG 验证 Provider 原始 ID/名称/状态及两次写入。随后全量 race 通过。该 HEAD 最后仅修正测试对“目录显示名称”和“Provider 原始名称”的不同语义，保留并加强两侧断言，未放松门禁。根此前独立历史/观察/101同库停重建/双向来源开关/HTTP及同步欢迎语回归证据继续有效。

2026-09-06 10:57:42 UTC 独立 squash 合并 #169，main `8ec5072d169c25abd25f80e29cb9f6222834b320`。未向 #168 集成。标签新版本的自动部署尚待核实，未启用 Provider 开关、生产导入或真实企微写入。

配置 #170 的 main 流水线 34025458810 已于 10:46:24 UTC 全部成功。根于 10:56 UTC GET 生产 /readyz，得到 release_sha=`5494537fb4d95a916c2754a3c1387335905d2861`、status=ready。此为代码部署证据，不代表管理员已发布生产业务配置或完成真实消费者业务验收。

| 板块 | 实现 | 测试 | 审核 | 合并 | 部署 | 真实业务验收 |
|---|---|---|---|---|---|---|
| 负责人 | 旧页面/Excel/选择器/执行结果收口中 | 后端专项通过，完整浏览器待交 | 未通过整板块 | 未合并 | 未部署 | 未进行 |
| 标签 | 本轮代码范围完成 | PG/恢复/协议/历史/Chromium/CI通过 | 通过准确6f4b63c | #169已合并 | 自动流程待核实 | 未进行 |
| 配置 | 本轮代码范围完成 | PG/消费者/历史/Chromium/CI通过 | 通过准确07b0af3 | #170已合并 | 5494537线上ready已核实 | 未进行 |
| 商品外推 | 业务配置/旧协议/Host/历史闭环收口中 | paid根PG/race通过；新Host/完整验收待交 | 未通过整板块 | 未合并 | 未部署 | 未进行 |
| 开放平台 | 旧授权模板与数据范围/接口装配进行中 | 部分权限专项通过；56接口完整流程未齐 | 未通过整板块 | 未合并 | 未部署 | 未进行 |

## 根独立增量复核（2026-09-06 11:19 UTC）

- 开放平台本地 checkpoint `5f2b3b6628ac8f77a5277ba9f8bc5453da58c0fa`：根独立 clone/随机真实PG数据库运行 TestOpenPlatformMachineManagementPostgreSQLJourney -race -count=1 通过，无skip。覆盖两个调用方完整 grants、管理列表、并发轮换/停用和 audit 失败回滚；修复未关闭外层 rows 又查询 grants 的 pgx conn busy。日志 aicrm-five-open-auth-5f2-root-review.log。该证据仅覆盖授权管理增量，不等于完整56路由、历史和浏览器验收；本地checkpoint不冒充GitHub已推HEAD。
- 商品外推 PR #172 准确 `424b805277765744ba9b121ac5c3fa30d54f16bf`：根独立PG/race通过 BusinessParametersRoundTrip、FirstBusinessSaveCAS、UnconfiguredPaidOrderPlansDisabledCommercePushOnce。首次无配置revision0，两管理员保存只有一个成功；无配置paid事件保持planned_disabled/replay幂等且无EER。日志 aicrm-five-push-424-root-review.log，根临时数据库已清理。HTTP/Store/receipt使用无损数字后，浏览器textarea仍须修复JSON.parse导致的数值损失；历史、真实Chromium和制品闭环继续，未准予整板块。
- 标签 main8ec5072 流水线34028859329的check已通过，自动deploy进行中，尚未核对新版本线上ready。此前配置main5494537上线核实仍有效，不以当前部署开始作为标签已上线证据。

## 当前审核修正（2026-09-06 11:54 UTC）

旧供体 remote 再次核实为用户提供的 https://github.com/qianlan33333-png/AI-CRM.git，冻结提交仍为 dd8d60dd8ddb983aca2ec88cc9e65a9f7563f79f，不临时改成浮动分支。

- #171 本地已完成对齐main8ec5072，处理实际Composition编译遗漏后才重跑全量CI。旧员工选择器、Excel模板/CSV与.xls别名、全量范围均按旧供体修复；新的全量范围PG专项已由执行者通过，根审核及真实Chrome仍待准确最终HEAD。
- #172 c90a7c2 CI34030684616准确失败为 TestPostgreSQLCommerceFundsHTTPJourney: signed commerce provider deliveries=0。根定位到商品业务参数与受控目标policy摘要边界不一致，已派修复；不得删目标撤销检查或重读当前商品配置替换旧任务冻结载荷。之前687/424的局部通过不能覆盖此新回归。
- #173 ba0155a 修复缺WeComScope导致普通后台装配失败，以及原operation-cycle固定token（包括含点token）/AI签名与通用机器鉴权的路由归属；需最新LinuxCI与真实旧handler协议回归。历史导入尚有两项阻断：来源Git SHA不能代替独立业务快照ID，跨批次源行须全局幂等；旧owner_scope中的客户数字ID不能直接解释为V3 OneID，须经既有可信映射，未映射不可因轮换密钥而激活。
- 标签main34028859329 check成功，自动部署仍处于Install versioned release；未记录为已上线。配置5494537的此前线上ready证据有效。无人工生产配置、历史导入或真实Provider操作。

## 最新独立复核与部署（2026-09-06 12:08 UTC）

- 标签main流水线34028859329全部成功；根12:07 UTC GET https://id-dev.youcangogogo.com/readyz，release_sha=8ec5072d169c25abd25f80e29cb9f6222834b320/status=ready。代码部署完成，真实打标/去标与生产历史导入未做。
- 负责人c7aaade根真实PG/race通过全量范围本地Owner优先、仅企微观察客户、截断拒绝、无企微ID本地Owner、CustomerSync/Welcome；标签HTTP最初因根review环境缺jsdom未执行，补测试依赖后独立通过。日志aicrm-five-owner-c7-root-review.log及aicrm-five-owner-c7-tag-recheck.log。
- 负责人准确7d84b1577a5833874dc2085f57096953041284ab仅补安装路径/正负例及Chrome布尔参数解析；根完整安装契约、旧.xls兼容、冻结HTML/选择器哈希通过。不能使用曾误报的7d84b152...完整SHA。Linux CI34031927510仍失败，准确为Owner Chromium local_only初始化错误，未到完整执行/回读；本机Chrome因Darwin启动限制明确SKIP，不能算浏览器通过。
- 开放平台本地207775212458603a482b94d3e9e990affba6a40f根独立真实PG/race通过历史CLI与管理Journey，无skip。覆盖跨快照幂等、同号客户限制隔离、旧group_broadcast/Direct映射和管理回读。该证据仅为历史/授权增量；目标已存在client的排除回执verify仍有具体修正要求，56条业务路由未齐，未通过整板块。

## 审核检查点（2026-09-06 12:40 UTC）

用户再次确认旧仓为 https://github.com/qianlan33333-png/AI-CRM；根核验只读供体 remote 与固定 SHA dd8d60dd8ddb983aca2ec88cc9e65a9f7563f79f 一致。继续沿用已有五 PR，不重建总集成。

- 负责人 #171 已推准确4688bad，CI34033617529进行中。根独立通过真实context路由授权投影与仅负责人页允许旧内联样式的CSP测试。Linux测试新增实际computedStyle和员工选择器可见性断言；Darwin Chrome启动失败明确SKIP，不当作浏览器PASS。
- 外推 #172 的9b2d903已由根独立通过真实PG/race完整CommerceFunds HTTP Journey及历史CLI。新fbb7132响应事实回读/1202d1a历史修正尚未全板块验收；根进一步要求cancelled/blocked无响应证据时保持unknown，因为旧系统允许已failed_retryable任务取消，终态不能证明从未调用。
- 开放平台 #173 根独立通过2077752历史提取/重放/核验及管理，ede9c42已有目标排除核验。a6a4404将订单customer约束与reference放在同一Owner查询，c79ac2e冻结机器actor而不伪装管理员；仍是局部实现。开放平台完整路由、页面、历史和运行装配继续同一PR收口。

本检查点没有新增合并或生产操作。标签8ec5072、配置5494537既有代码部署证据有效；三项未合并，五项真实业务验收均未进行。


## 2026-09-06 13:20 UTC 审核增量

- 最新用户明确要求完成上线，并加入PR164新壳。五业务仍各自完整PR，164独立，不向168集成；本表顶部为当前检查点，前文是带日期的历史记录。
- #171真实Linux已通过local_only含实际Excel下载，wecom_then_crm曾因两处动态表达式语法失败；96b79b8定点修正并预检，仍须新CI完整结果。
- #172根在b40c82b真实PG/race通过完整Funds HTTP和历史CLIextract/apply/replay/verify/drift。0fd897e实际构建Host后消除503，当前失败是页面保存未结束；不能以专项PG通过覆盖浏览器失败。
- #173根在05ffb57真实PG/race通过Archive machine projection与Radar links disabled/keyset行为；d7e9ba2新增历史提取尚未通过根审，旧SDK实际保存{seq,encrypted_record,decrypted_message}，不能用手写顶层SDK fixture代替真实源。0098 Archive历史投影为owner保护的TEXT，并非字段加密；0099 Survey仅保留历史读取字段，不能升级成OneID证据。
- 生产13:01 UTC只读核实main8ec5072、aicrm与effects-worker active。通用客户标签Provider及商品外推Provider未启用；外推受控目标/载荷密钥和开放平台JWT密钥尚未配置。后续必要配置在验收后按最新上线授权准备，真实写验收使用明确测试对象。
