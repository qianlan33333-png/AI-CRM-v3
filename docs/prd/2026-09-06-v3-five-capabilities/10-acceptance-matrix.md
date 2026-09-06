# 五项验收与审核矩阵

基线50b86c8，更新2026-09-06。只按证据更新，不把计划/派发当完成。

开放平台权威范围更新（用户最新明确指令）：#173采用03-open-platform.md与ADR0010定义的V3原生最小方案。旧56路由清单只保留历史，不再作为开发、审核、上线条件；未列V1旧路径不挂载并返回标准404。保留已验证Access/OAuth2/grant/CIDR/audit/MCP/OneID/history/machine actor基础，REST /open/v1与MCP /mcp共享六个Operation处理器。

| 板块 | PRD | 当前实现证据 | 完整板块 PR / 最新已知 HEAD | 根审核与剩余项 |
|---|---|---|---|---|
| 负责人迁移 | 01 已批准 | 旧流程/PG/恢复/协议/历史通过，实际Composition修正后Linux Chrome收口 | #171 `7772d013398c36a92b7cb684a536ea4b34cfb882` | 88b实际失败为provider line retryable_failed，已定位Provider直连需事务的Store；c95改现有Customer UoW Adapter，777新增真实HTTP→River→Provider→Owner更新测试；等待准确HEAD完整CI |
| 通用客户标签 | 02 已批准 | 根PG/race、历史/101恢复/协议、真实Chromium与全CI通过 | #169 `6f4b63c53c000cbc1c523ece78a9fec37147b735` 已合并 | main8ec5072于13:01 UTC再次核实线上ready；代码已部署，13:23 UTC通用标签开关已启用并核实API/Worker实际加载；真实业务验收未进行 |
| 配置中心运行生效 | 05 已批准 | 根PG/race、实际消费者/历史/制品、真实Chrome及CI通过 | #170 `07b0af371db7a97620924a34ee5975a74c775d39` 已合并 | 已随5494537及后续main8ec上线；生产业务配置发布与真实消费者业务验收另记 |
| 商品／订单外推 | 04 已批准 | paid、expiry、配置CAS、历史与协议根PG/race通过，真实Chrome收口 | #172 `ff8e51a1b19de83d3e78b8986e38ece150e2c8ad` | 普通商品保存/重载/测试投递已推进，当前周期商品Host未呈现；另已发现main旧保存收据摘要升级兼容缺陷，要求真实PG升级重放修复；未批准整板块 |
| 通用开放平台 | 03原生V1及ADR0010已批准；03a仅历史 | 保留现有API Client/OAuth2/grant/CIDR/audit/MCP/OneID/history/machine actor | #173远端ba0155a（已刷新核实），本地e221801e55b206dac89ad0324cad4d4d7a3a2cf7；执行者正在推送当前成果 | 仅审6Operation；活动message/survey/radar/order稳定Port、AI待审阅计划/状态、REST/MCP一致、旧path404和PR164管理Journey；56旧路由停止派工和验收 |
| 新前端壳 | 12 已批准 | 沿用原PR164工作，在独立clone合入main8ec | #164 远端`c8819d7a9b837c00116682e860310717e5f94b20`，本地a96b162 | 现有远端CI在donor manifest失败；待修正确V3适配归属、完整制品、模块Host路由、侧边栏CSP/素材可见性，最终组合浏览器和部署待验收 |

共用验收：完整路由/Composition、冻结供体复用、PG原子性/并发/重启、身份权限、未知结果、历史零新效果。各PR需链接实际日志/测试/浏览器证据，跳过与Mock明确标识。

## 独立交付与合并记录

按用户最新要求，整板块验收通过后独立合并；不再以总集成 PR 为交付单位。#168 停止接收业务 HEAD，已关闭并保留阶段记录。它此前只纳入文档及共用修复，没有五项业务实现需撤回。

- 共用修复 #167，准确 HEAD `cbac1f6bdb2b868985c577ba0cff11482430a19e`：完整 CI、根独立真实 PostgreSQL race 测试与审核通过。已独立 squash 合并 main：`53c1c62e7db7924b979aa11fd8345d969fadf4ec`，既有自动部署已成功，生产readyz核对到53c1c62；不代表五板块生产业务验收。
- 配置中心 #170、客户标签 #169 整板块代码验收已完成并独立合并；其余三项仍未完成，单项证据通过不填为整板块完成。
- 每个板块合并前记录最终准确 HEAD、真实 PG/浏览器/协议/历史证据、CI及 review 结论；合并与既有部署流水线结果另外记录。
- 生产状态分别记录：标签通用Provider开关已于13:23 UTC启用并核实加载；配置发布、真实转接/打标/外推、生产历史导入尚未执行，不能由合并状态推导。

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

## 当前独立复核与修正（2026-09-06 13:58 UTC）

- #171 df862修复转接结果读取器装配及冻结摘要匹配，根实际HTTP/PG/race通过。88b真实Chrome暴露执行前直连Store缺事务，c95改用既有Customer执行Adapter；777新用例从实际HTTP预览确认进入River/Provider并核对本地Owner。旧失败不继续重复派发，最终以777后续CI实际流程为准。
- #172 4505根独立真实PG/race通过HTTP disabled完整形状、BusinessParametersRoundTrip、FirstBusinessSaveCAS、ExpiredPaidOrderPlansConfigExpiredCommercePushOnce、CommerceFundsHTTPJourney，0跳过，日志aicrm-push-4505-root-review.log。expires_at_ts沿用旧版，过期paid只保留planned_config_expired事实且不产生EER。ff8e真实Chrome已推进普通商品流程，周期商品Host失败尚未关闭。
- #173 Segment a2a1da0+d0b2621两提交经根全包真实PG/race审核0跳过，覆盖升级前旧收据重放与两个机器调用方相同key隔离，现由Openlead合入92b4aef/9df5adb。Archive48fe真实旧wrapper的全历史CLI PG/race通过；Survey35f旧Union投影导入PG/race通过；19351ec Survey原生/历史混合读及实际PG Access/JWT→Executor spy测试通过，spy不代表端到端Survey验收。根发现直接请求Union scope未限定Survey来源，退回修正后才能接入历史查询。
- 生产只读源事实：旧负责人迁移结果34行；商品外推配置31条，其中12启用且均HTTPS、均未到期。源配置历史导入不等于V3运行配置启用，仍需稳定产品映射与受保护target槽位。

2026-09-06 14:10 UTC增量：#171 97f70428限定最终PG断言的b/l列名，CI已越过实际Go/Chrome流程，后续web/scripts/e2e.mjs仍引用旧临时data-scope表单而失败，正改为冻结Picker/文件实际流程并要求本地完整前端回归。#172 469914a精确main8ec旧binding收据重放根PG通过；根并发专项实际出现successes=2/conflicts=0/events=2，已定位Product LEFT JOIN配置和行锁处于同一statement导致等待前快照，要求分开锁与读并加写入CAS，不以偶尔通过抹除竞态。469真实Chrome周期页面仍未挂载Host，要求优先核对实际Composition路径及脚本入口。#173 e221801已限定Survey历史Union selector到明确Survey scope并返回稳定409，源审核通过，完整路由旅程继续。

## 原生V1现行验收矩阵（覆盖旧开放平台记录）

| 项目 | 当前基础 | 完成证据要求 |
|---|---|---|
| 共享Operation Catalog | 原有MCP与授权可复用 | 六operation_id/REST DTO/MCP schema/error/capability/幂等契约冻结，Composition可用性一致 |
| 授权管理与能力目录 | 现有Access真实PG增量已审 | OAuth2 client_credentials、细粒度capability、实时撤销/轮换、CIDR/代理、数据范围、新UI |
| OneID解析及客户上下文 | scoped解析/Customer Port已有 | 同一处理器处理REST/MCP；缺scope/pending/conflict/no-create、范围前置校验 |
| 客户活动 | 已有Archive/Survey/Radar/Order读取增量 | 四Owner Port真实PG，稳定cursor/权限/类型化payload，依赖失败不伪空成功 |
| AI审阅计划和状态 | 现有AI领域审批/任务链 | machine actor、同UoW计划/receipt/audit、同key同结果/漂移拒绝、审批不可绕过、重启后状态读回 |
| PR164管理页 | 新壳现有开发继续 | client创建/一次secret/grant/轮换停用/audit/V1catalog与真实只读调用；不展示56目录 |
| 历史与移除旧路径 | 既有停用导入基础保留 | 不恢复旧token/secret，旧路径标准404；真实PG/REST/MCP/Chrome/fullCI |


## 2026-09-06 14:45 UTC 原生 V1 独立审核检查点

- ee9f84f5019764610e72e8f3d0dcd18f35ec3e49 的共享 REST/MCP 路由、目录权限、作用域 OneID 和最终 Composition 的旧路径 404 经根 PostgreSQL/race 通过，0 跳过，日志 `aicrm-open-ee9-root-review.log`。四类活动、AI Host、调用审计、管理页尚待完成。
- AI Owner 075131eaa01af8aa531b5dcf16a6a03d16cfb5c1 + acb45a929f27e2b18b88d85b03f29dbcdabd9ab3 的全部 AI PostgreSQL/race 经根独立验证通过，0 跳过，日志 `aicrm-aiowner-acb-root-review.log`。0100 的 SQL NULL 约束缺口已修复；旧 human 收据字节、机器间隔离、并发重放及同 UoW 回滚已验证。已批准纳入 #173，完整 Open 审计和协议仍待验证。
- #171 的 8f7bafd 已完成仓库检查和两套 Linux Chromium，但最终 race 被 check job 20 分钟总超时终止。官方 annotation 明确为超时。3dfb96c1b3026ca7a6c774712d994dc87f5511e8 仅将时限调到 30 分钟，保留所有门禁，CI34039811444 待完成。
- #172 的 7ae65f0a1778d2757cdcd5826513b098fee3d1f7 持久 HTTP 事实专项经根 PostgreSQL/race 通过，覆盖受控 synthetic 效果与加密大整数载荷；CI 已越过普通/周期配置保存重载，历史订单页等待因导航中 document.body 尚未建立而抛异常。38e71c779887780e8df7cbc260d8e4616c6697f0 定点增加空 DOM 就绪保护，最终 CI 尚待通过。
- #164 继续将冻结供体恢复、最小接线移至 V3 Host；开放平台管理展示原生 V1 capability catalog，不再展示旧 56 接口。

本检查点没有新增合并或部署。负责人历史加密快照已准备，尚未应用；生产标签 Provider 启用沿用先前核实证据。


## 2026-09-06 15:28 UTC 根审核检查点

- #173 a173f50928177021d5a887e4e5cb7aa7e04fb516：服务端仅允许 V1 capability、presence PATCH、同事务授权版本撤销与审计，根真实 PostgreSQL/race 控制面旅程通过，0 跳过（aicrm-open-a173-root-pg.log）。该接口检查点可供 PR164 接入，整板块尚未验收。
- #173 99e3aa01fa8299e72a7e318ca32fc7eda26d4059：根真实 PostgreSQL/race 验证 REST 创建待审计划、重建连接/服务后 MCP 状态、跨调用方 404、AI 与 Access 审计故障同事务回滚，0 跳过（aicrm-open-ai-99e-root-review.log）。55a8e5c 后续跨协议重放/权限撤销待根核验；客户与四类活动真实 Port 组合旅程、管理 UI 和最终 CI 待完成。
- #171 b0939dc4fa893abb3b041e15fe567281433ce3cb：根独立真实 PostgreSQL/race 两项通过，含恰 100 条首次提交、19,900 条等待及重启完成 20,000 条（aicrm-owner-b093-root-review.log）。修复只在测试第一 Runtime 阻止后续段提前执行，不放宽断言、不修改生产分段规则；完整 CI 仍运行。
- #172 73cdaa35293ca2976811f72dae548db5c5722951 完整 CI34040894501 成功，包含 Linux Chromium。随后真实源提取发现多执行任务关系；01f1+514 修复 0/1/多关联的完整密封事实，并保持真实旧 V1 wire 与摘要。根全包真实 PostgreSQL/race 通过（aicrm-push-history-514-root-review.log），已批准纳入 c031ca5604e13f58d71038f8adee6b3bd7e44ac8；需该最终 HEAD 完整 CI。
- #164 f129 发布闭包根独立构建/缺文件拒绝验证通过，41 管理页、73 运行资产。d1f95c1 的侧边栏 Survey 实际 Total 源码方向正确，但根真实 PostgreSQL 发现新增 fixture 时间参数被推断为 interval，已退回显式类型修复。此前执行任务的通过声明不能覆盖准确 HEAD 的失败；当前不标记完成。

本检查点没有新增合并、部署或真实 Provider 业务验收。


## 2026-09-06 16:06 UTC 根审核检查点

- #172 `c031ca5604e13f58d71038f8adee6b3bd7e44ac8` 完整 CI34042310198 成功，包含 Linux Chromium；根确认最终树与已审历史修复一致后合并为 main `65d9b0dde12244b9ca21a73bacbb49008e24713e`。该 main CI34043258907 被既有人工群发浏览器测试的固定250ms等待阻断，部署未执行，不能称为已上线。独立小 PR #174 `abbfb4b6c16ec0cd32dd12e0b787f3c5f2970c49` 改为有界实际页面就绪等待，并在真实 HTTP fixture 加600ms延迟；根真实 PostgreSQL/race 0跳过通过6.147s，完整CI待完成。
- #173 `090889a891614dcb6da726ea6d073b39cc9411ec` 是当前准确审核 HEAD。根已核对基于 main65d 的完整 rebase range-diff，原审核的 OneID/四类活动/AI/控制面与0095外推均保留。88459181 全迁移实际 Composition + Management 真实 PostgreSQL/race 0跳过通过16.185s。090889a8 修复合法未知 client ID 轮换绕过 OAuth 预认证配额，只按来源共享 Access 既有持久 bucket；根专项与真实 HTTP/PG 0跳过通过。完整CI及与 PR164 组合的真实管理浏览器旅程仍待完成。
- #171 `2eb9c3f0e4f23e6fd41388518e42aac780679065` 修复结果导出调用遗漏 trigger，源码已审，准确 SHA 的 Linux CI34043950115 正在执行；与 main65d 有冲突，须保留双方装配后再跑最终 HEAD。旧4b失败不能归给2eb，也不能把2eb当前无 PR check 显示当作通过。
- #164 `2b591d80e9ba88a7ea9d479af2f6b8ff1d581258` 已合入 main65d。81fecb5 用真实 Media Reader 装配修复侧边栏503；根真实 PostgreSQL/race 校验 bootstrap、102条问卷总数和侧边栏完整读取均通过2.63s。原生V1 Host已修复TTL60..3600、时区漂移、凭据复制失败与激活结果未知处理。仍须组合173实际浏览器与最终CI。

实现、专项测试、审核、合并、部署和真实 Provider 业务验收分别记录。本检查点仅172新增合并；生产仍使用先前已核实的8ec版本。


## 2026-09-06 16:19 UTC 增量

- #174准确abbfb4b完整CI34043961528成功，已合并main05045c645f95d269b624771ceb215713e3300f59；主线发布run34044888183进行中，尚未确认部署。
- #171普通merge65d为9d0b75af6d2c82cd20dadc87772885b16d2f36a2，0092/0095、双方历史工具和Provider装配保留，源码冲突审核通过，准确CI待完成。
- #173090 CI暴露既有自动化导入器漏填0097 actor。用户侧边补丁仅两个导入文件；根独立PG/race四包0跳过通过（3.073/1.770/2.025/1.229s），原失败用例1.59s通过，架构910文件通过。选择复用AdminMutationActor及管理员42的五表dry-run/apply/replay断言版本，替换并行手拼actor重复修复；不增加六Operation以外业务。最终提交及完整CI待核验。
- #1647cca25da68fe079c50a0e760de4e5cfb7b67dc31修复Chromium测试工作目录导致web/dist回落旧壳，增加外层Host/资产预检；源码审核通过，Linux浏览器仍待证据。
- 标签305条及配置8条生产历史已apply/verify，具体pending/excluded明细见11；没有新的真实Provider业务操作。

## 2026-09-06 16:58 UTC 收口核对

- #173准确fb9dbb4d048c18cd8bd546ddbfb24110d1f55492完整CI34045870718成功；用户actor导入补丁已用AdminMutationActor原样纳入，根独立四包PG/race及空CIDR协议复核通过。仍限定六个V1 Operation。
- #164准确4fcdeb4d6766e45ed1b9c465e479ef1b127334a1完整CI34045781073成功。尚待最终173/171组合及新壳开放平台Chromium收口，不等于最终发布包完成；组合测试90秒等待超时仍在定点定位。
- #175 f9a0c95的摘要绑定、逐源映射/商品code/version核验，根真实PG/race三包通过。但实际旧源交叉预检证实拟恢复的3条启用配置属于商品缺失，追加不适用，停止执行。此前27条/12启用恢复预期撤销，按11部署待办的24已映射/9启用与7条历史分类处理。
- #171 cc34d6bc6be40bcf395f77b01891fca1c37f38c4完整CI仍在最终race；main05045部署仍传输，线上未声称更新。

## 2026-09-06 17:18 UTC 商品外推代码已部署、历史已对账

main05045c645f95d269b624771ceb215713e3300f59的Linux check job101518074524已成功。自动部署跨区上传缓慢，根在独立精确HEAD构建全部20个Linux二进制、94项迁移及前端资源，制品226文件/77,118,012字节；归档SHA256 `7173309f191848d72c705276ca375b2eb4d080f6156f818bdde5f89e651835f0`。本地cgo runner使用Zig 0.13，未声称与CI GCC字节相同；生产服务器已验证SHA及Linux loader兼容。原流水线34044888183仅在check成功后取消慢速deploy，根使用仓库原installer/run983完成安装；这是人工部署证据，不把取消的整条workflow写成成功。

17:15公网readyz、current symlink、aicrm与effects-worker的实际exe均为05045，原outbound/customer-tag/wecom开关均保留true。普通HTTPS管理员登录、运行配置读取和退出通过；有效配置仍revision0/environment_default/max_recipients1，未发布新值。CommercePush Provider仍false，新壳#164和负责人#171/开放平台#173尚未上线。

外推旧源manifest c8c20c8c1ef30bb19296eb01a7274a39c8997fb4b27375d3818d9bb8b784fbce已用当前发布的迁移器apply并verify：800输入=406 imported+10 pending+384 excluded。生产SQL逐项回读：24配置/382投递已关联，7配置/3投递product_mapping_unavailable，384旧domain_event_outbox以legacy_domain_event_not_replayed保留排除事实；live commerce intent及commerce_product_push effect均0。

运行配置受保护准备文件为 `/var/tmp/aicrm-push-history-20260906T150000Z/runtime-prepared.json`，24配置、9原启用、7排除项、9受控目标；payload key已生成并封存，尚未应用runtime或保存业务配置。PR175已关闭且未合并/部署/apply，明确撤回不适用的追加方案。
