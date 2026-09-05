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
- PR137：除前置0067，需0073合成测试快照、0074安全执行回执事实与0075外推效果kind允许约束，均在启用前核对。0075仍在开发验证中，不能用已有父PR134绿色CI推定整个提交外推已可用。
- PR139：迁移0071/0072及Linux独立SDK Runner须就绪。配置名为 `AICRM_WECOM_MESSAGE_ARCHIVE_ENABLED`（默认false）、`AICRM_WECOM_MESSAGE_ARCHIVE_SECRET`、`AICRM_WECOM_MESSAGE_ARCHIVE_RUNNER_PATH`、`AICRM_WECOM_MESSAGE_ARCHIVE_LIBRARY_PATH`、`AICRM_WECOM_MESSAGE_ARCHIVE_PRIVATE_KEY_PATHS`、`AICRM_WECOM_MESSAGE_ARCHIVE_PAGE_LIMIT`、`AICRM_WECOM_MESSAGE_ARCHIVE_PAGE_BUDGET`。核对既有企微回调开关/Corp/Agent、官方SDK包和库摘要及版本化私钥路径。SDK启用前，已导入文本读取与真实通知拉取分别验收；本轮不复制私钥或获取生产正文。

上述变量仅列名称/对应关系，尚未应用。最终集成完成后再以组合提交补全迁移顺序、归档SDK安装和其它板块具体项。
