# 生产部署待办（发布候选冻结前不执行）

这是一份交接清单。用户已经批准逐 PR 即时审核和非生产集成流程，但该批准不等于生产部署授权。两套系统独立，无停旧或切流步骤。总集成 PR 达到九板块完成标准后，必须先冻结准确发布候选 HEAD，并把本清单补成可执行上线包，再由用户对该具体候选作最终上线确认。

## 配置来源映射

| 能力 | 旧环境已发现配置类别 | V3 需要核对 | 本轮状态 |
|---|---|---|---|
| 企微目录/员工 | Corp、应用、客户联系凭据 | scope、权限、API 与回调用途 | 只读证据，未复制 |
| 公众号 OAuth / 问卷 | MP AppID/Secret、OAuth scope、可信域 | V3 公共域、redirect URI、签名状态和会话密钥 | 未应用 |
| 微信支付 | 商户、支付 AppID、API v3 key、私钥/证书、公钥、notify URL | 与 V3 支付身份和回调一致，证书可用 | 未应用 |
| 微信小店 | AppID/Secret、回调 Token/API 配置 | V3 中仅启用旧版真实支持的能力 | 未应用 |
| 会话存档 | Archive Secret、私钥路径、SDK 库路径 | SDK/OS/ABI、publickey_ver、调用 IP、通知入口、私有媒体目录 | 未应用 |
| 自动化/AI/群/欢迎语 | 功能开关、发送与素材准备配置 | V3 自身业务范围、测试目标、实际生效配置 | 未启用 |

不回显 Secret、Token、密钥内容或原始用户身份。配置存在并不证明适用于 V3。不得覆盖整份环境文件或直接复用旧 notify URL 造成错误回调归属。

## 部署阶段才执行

- 核对总集成 PR 的唯一发布候选 HEAD、实际制品、数据库迁移顺序与兼容性；取得用户最终上线确认后，只合并该总集成 PR，不逐个合并和发布板块 PR。
- 对各领域一次性历史导入明确快照边界、dry-run 及对账，再确定生产 apply；历史终态不启动新效果。
- 配置 V3 独立 OAuth、支付回调、企微通知与调用 IP，确认不影响旧系统正常运行。
- 使用用户指定的测试客户/群/商品和明确内容进行真实业务验收；模拟 Provider 证据不能替代。
- 客户同步持续成功、欢迎语真实期限/回执、存档真实通知与 SDK、购买支付退款/权益等逐板块验证。
- SDK 授权/动态库、云侧配置、签名域或独立回调存在外部限制时，给出具体阻断及解决项，不替用户暗改旧环境。

本次发布完成后另行提交一个小型交付机制改进：将“代码合并”和“生产部署”拆成两个受控动作，使后续已审核 PR 可以及时进入 main，而生产部署通过手动发布或受保护环境审批触发。该改动需独立审核，不能在当前发布候选中临时改变部署语义。

## 每个开发对话需补充

必要环境变量名称（不含值）、依赖的 schema/素材版本、默认 disabled 开关、迁移命令与安全检查、生产验收步骤及已知限制。不得把基础架构治理项目加入上线前置。

## 已完成 PR 对应的具体待办

- PR133：核对客户目录读取权限与原有同步任务运行配置，真实全量/增量结果另行验收。错误分类和恢复测试不代表生产已重新同步。
- PR134：迁移0067先就绪；`AICRM_SURVEY_DATA_KEY` 沿用受保护数据密钥；新增 `AICRM_SURVEY_COMPLETION_PROVIDER_ENABLED` 默认false，受保护 `AICRM_SURVEY_COMPLETION_TARGETS_JSON` 映射引用、端点版本、签名客户端和明确身份kind/scope。它还依赖现有总效果开关。问卷OAuth的 `AICRM_SURVEY_OAUTH_ENABLED`、APP_ID、SECRET、OPEN_PLATFORM_ID和SCOPE分别核对域名与授权作用域，不能从“变量存在”推定可用。
- PR135：迁移0066包含Channel欢迎意图与原共享效果队列约束变更；先准备既有素材和密封grant所需配置，再考虑开启 `AICRM_CHANNEL_WELCOME_PROVIDER_ENABLED`。既有shared River运行时同时消费普通outbound与outbound_welcome，必须核对实际worker角色在运行。旧无可信首次期限的welcome任务将明确不发送；过期不通过营销补发伪装成功。
- PR137：除前置0067，需0073合成测试快照、0074安全执行回执事实与0075外推效果kind允许约束，均在启用前核对。PR137准确HEAD 807873c的实际PG/River与协议检查已经通过并纳入；部署时仍需应用对应迁移和核验真实配置。
- PR139：迁移0071/0072及Linux独立SDK Runner须就绪。配置名为 `AICRM_WECOM_MESSAGE_ARCHIVE_ENABLED`（默认false）、`AICRM_WECOM_MESSAGE_ARCHIVE_SECRET`、`AICRM_WECOM_MESSAGE_ARCHIVE_RUNNER_PATH`、`AICRM_WECOM_MESSAGE_ARCHIVE_LIBRARY_PATH`、`AICRM_WECOM_MESSAGE_ARCHIVE_PRIVATE_KEY_PATHS`、`AICRM_WECOM_MESSAGE_ARCHIVE_PAGE_LIMIT`、`AICRM_WECOM_MESSAGE_ARCHIVE_PAGE_BUDGET`。核对既有企微回调开关/Corp/Agent、官方SDK包和库摘要及版本化私钥路径。2026-09-05 已只读核实 V3 主机为 x86_64、Ubuntu 24.04.4 LTS、glibc 2.39；发布runner固定为Linux amd64 cgo构建，与该目标平台匹配，并继续通过固定官方SDK摘要和无凭据ABI health门禁。无凭据health只证明制品/动态库ABI可装载，不代表真实Provider通知、拉取或解密验收。SDK启用前，已导入文本读取与真实通知拉取分别验收；本轮不复制私钥或获取生产正文。

上述变量仅列名称/对应关系，尚未应用。最终集成完成后再以组合提交补全迁移顺序、归档SDK安装和其它板块具体项。

- 群运营生产前置：官方92698指定chat_id_list要求支持该字段的企微终端（4.1.10+）。接口创建成员待操作任务，provider_accepted不代表送达；独立查询核验msgid与冻结发送人/群的实际结果。不得用allow_select作为群目标保证；本轮未创建真实群发任务。
- PR148：0078需在群运行及读取前就绪；`AICRM_GROUP_OPS_PROVIDER_ENABLED`默认false，群发送、目录及结果读取权限按独立V3环境验证，既有`AICRM_GROUP_OPS_WEBHOOK_SECRET`仅在批准的入口配置。源码与本地协议/PG通过不代表真实群任务已发送。
- PR151：0080与`migrate-media-legacy-materials`工具随制品发布；先冻结旧素材记录和实际V3素材映射，inspect/dry-run/apply/verify按同一快照摘要执行，未映射的素材不会被intake自动创建。核对`AICRM_AI_ASSISTANT_INTAKE_ENABLED`、`AICRM_AI_ASSISTANT_DISPATCH_ENABLED`（均默认false）、`AICRM_AI_ASSISTANT_INTEGRATION_KEY`、`AICRM_AI_ASSISTANT_INTEGRATION_SECRET`、`AICRM_AI_ASSISTANT_INTEGRATION_ACTOR_ID`与`AICRM_AI_ASSISTANT_PROVIDER_PERMISSION`；Scope沿现有Corp与`AICRM_SURVEY_OAUTH_OPEN_PLATFORM_ID`配置，不能错用旧域或跨平台身份。旧调用方路由为`/api/admin/ai-assist/review-plans`，仍必须走机器签名校验；生产外部调用方及真实Provider验收独立记录，不导入旧AI执行。

- 0081已作为群运营审核增量纳入：部署清单包括0078/0081，未配置Webhook的空引用允许多计划；非空仍唯一。本轮仅本地合成发布检查，不执行生产迁移。0082/0083/0084需各自源码与CI审核后再进入最终清单。


- PR153历史增量已审核：0082随总集成候选发布。现有migrate-v2-config-definitions新增history-extract/inspect/dry-run/apply/verify模式；冻结来源版本与五类历史记录，使用独立受保护密封密钥文件（32字节值的base64、权限0600），逐行结果和源/目标摘要守恒。此处是生产阶段待执行清单，本轮只运行合成PG/HTTP验证，未从生产提取聊天/客户正文或apply历史。

- PR154共享HXC字段已审核：0084需在既有HXC刷新前就绪；复用现有受控只读MySQL连接与OneID配置，不新增凭据或同步角色。部署后先核旧代字段unavailable，再以一次正常授权刷新验证新代、两消费者和实际数据量耗时；本轮EXPLAIN只验证源查询可执行，不代表生产耗时已测量。

- PR143已审核的03/04联合候选增加0076不可变checkout、0077公开券slug、0079原会员表视图/协作/分享；前置0068/0069/0070必须齐备。0088联盟字段已按270d8f98审核，随候选核对实际制品与schema readiness；已有历史快照缺联盟字段继续保留未知值和旧摘要。cmd/migrate-commerce-history已用冻结合成快照实跑apply/replay/reconcile，生产仅在冻结源摘要、逐行隔离/对账与零效果要求明确后执行，不以测试替代真实旧快照dry-run。

- PR158已审核：0090在问卷OAuth启用前执行，修正既有0018重定向CHECK且不改旧迁移。实际Survey Readiness核对修正后的约束；all/one有效回跳、外站/非法slug拒绝已通过PG16/race。部署仍核V3独立OAuth域/回调，不触碰旧环境。


## 唯一候选冻结时的执行清单

本节是最终交付要求，当前仍有未审源HEAD与05/06/07联合项，尚未冻结候选。准确HEAD和组合CI以10矩阵为准，禁止把一个已绿子PR当作完整候选。

1. 记录总集成commit、tree、全部来源准确HEAD和最终PG16/race/官方SDK ABI检查URL；核main未漂移，所有来源均已在总集成祖先链中。只允许总集成PR进入main，不单独合并来源PR。
2. 清点制品：现有workflow仅在main push/workflow_dispatch的deploy job构建Linux amd64正式包；PR检查不会生成可部署正式包，不能从deploy SKIPPED宣称已取得正式制品摘要。批准后唯一merge触发既有流水线，归档实际main SHA、aicrm-SHA.tar.gz摘要及包内release-files.sha256校验结果；无需为取制品提前触发生产workflow。
3. schema按既有migrate-platform的文件序列前向执行，复核实际已应用版本与本候选0066—0091差集（0091待源CI及总控审核后纳入）；每个编号的Owner见12文档。先核备份/恢复点、所需数据密钥、迁移锁和空间，不能假设CI空库等于生产升级验证。独立业务历史导入工具只随包提供，不由这次发版自动调用apply。
4. 配置逐项补入V3独立值并保持未验收Provider开关关闭；不覆盖整份环境文件、不搬旧域/回调。SDK库固定摘要与私钥版本映射、OAuth scope、支付商户与证书对应关系由部署阶段在受保护路径核对，文档不存值。
5. 按既有installer核release-files.sha256、迁移/二进制完整性、API readyz实际SHA及effects worker实际执行文件；保留每步结果。再进行指定测试对象的真实Provider与页面验收，成功条件仍分别核发送受理、实际送达、支付/退款对账及OneID归属。
6. 失败停止后续启用和真实业务验证，保留失败步骤、原SHA、原current链接及真实外部结果。现有installer回退current并重启旧二进制，不回滚已提交SQL或Provider效果；不能宣称这是数据库/业务回滚。新增迁移均前向处理，数据库恢复须针对记录的备份点单独制定；已发生支付/退款/发送不盲目重试。修复后由同一总控重新确认候选，禁止逐板块补部署掩盖失败。

配置来源读取、生产备份核验、实际旧快照dry-run/对账、生产配置应用和真实Provider验收均属于下一阶段，尚未执行；本轮合成PG历史测试与其结果不得替换这些条目。

- PR152审核后纳入0083/0085/0086/0087/0089：刷新模式/种类、WeCom完整负责人事实、人工AI计划关联与自动内容快照必须随同一候选应用；人工审批和自动发送仍沿既有各功能开关，不因迁移成功自动开启。PR1604982已核正式构建清单及缺文件安装反例，archive/commerce importer随唯一包交付，但生产数据apply不在installer中执行。

- PR143 e9e62809已审核完整sidebar历史核验与恢复；新增schema2捕获仅用于新提取，原schema1快照和摘要保持兼容。生产历史apply以原冻结文件/摘要重跑恢复，不能更换run-key掩盖中断；逐条目标/OneID/隔离与重算批次守恒通过后再标记reconciled。本轮仅合成PG执行，未读取或导入生产客户记录。

- 问卷0091已分配但尚未审核纳入：旧库ASCII测评键CHECK须前向兼容原中文/斜线键，具体源码、Readiness和安装证据待PR158准确HEAD通过；不可将编号预留称为已发布。
