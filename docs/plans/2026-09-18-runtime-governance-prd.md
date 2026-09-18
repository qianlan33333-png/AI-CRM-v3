# 新 CRM 长期运行治理 PRD

状态：按 2026-09-18 用户批准计划实施；验收事实另行记录，本文不构成部署或送达证明。

## 业务判断与架构分类

- OneID：只读规范身份事实与冲突统计，经 Identity Port；不新建、合并或修复客户。
- Persistence：AdminOps 拥有巡查、报告、问题及过程明细；内部持久任务复用 River。领域事实由各 Owner 的 Port 提供。
- External Effects：飞书属于 Provider 写入，新增 `adminops/feishu_ops_notification_v1`，小时报告与效果接受同一 PostgreSQL Unit of Work。来源、目标、载荷和策略均冻结摘要；发送超时保留结果未知，禁止盲目重发。
- 业务故障只报告和跟踪。唯一自动维护是经登记白名单和保护检查的纯过程数据及发布产物清理。

## 用户可观察结果

1. 管理员在统一工作台查看检查覆盖、当前异常、问题跟踪、报告、数据保留和容量；未知、过期与未覆盖不可显示为正常。
2. 每 5 分钟轻量检查、每小时完整检查，每小时 05 分向原飞书群提交一个唯一报告窗口。报告分开呈现上一完整小时流量和当前积压；未来预约任务不算积压。
3. 排查编号关联请求、版本、领域操作、任务、效果与收据；不记录 Token、Cookie、身份原文、手机号和原始请求。
4. 纯过程数据滚动保留 720 小时，在下一轮小时清理删除；业务数据及幂等、身份、资金、正式发送和对账依据永久保留。未解决问题也不延期保存原始日志。
5. 发布包保留当前版本和两个经产物校验且与当前 schema 兼容的回滚版本，额外保护运行中、发布中和固定版本。备份完全不在本次范围。
6. 变更识别 Owner、消费者及旅程；保持性测试覆盖 B 覆盖 A、乱序、重放、并发和重启；完整 CI 仍是合并门禁。

## 接口与权限

- `/api/admin/ops-inspections`：摘要、检查、运行、小时报告、问题、心跳。
- `/api/admin/ops-diagnostics`：错误聚合与关联链，仅暴露脱敏必要信息。
- `/api/admin/ops-retention`：固定策略、预览、执行记录、容量。不得接受任意 SQL、表名或文件路径。
- 复用现有管理员会话、权限、CSRF 与审计；普通用户不能读取明细或启动清理。
- `POST /api/admin/ops-inspections/runs` 返回 `202 Accepted`，表示已接受幂等持久任务，运行结果需读取详情；达到小时限额返回 `429` 与 `Retry-After: 3600`。接受、执行完成、报告提交与飞书送达是四项不同事实。

## 数据生命周期

独立规则、机器资源清单及 Owner 实现共同构成清理授权。未分类资源不自动删除。混合表仅删可丢弃载荷，永久保留业务事实、摘要、幂等收据；单并发、每批最多 1,000 行、短事务、锁超时，删除时重新检查状态与引用。River 终态记录使用原生 cleaner，三种终态统一 30 天。数据库可复用空间与文件系统实际回收量分别统计。

## 成熟方案与采用依据

- [OpenTelemetry 日志模型](https://opentelemetry.io/docs/specs/otel/logs/)：采用关联字段，不在首期建设额外日志存储平台。
- [PostgreSQL 16 清理维护](https://www.postgresql.org/docs/16/routine-vacuuming.html)：索引及小批删除，交由 autovacuum 回收可复用空间；自动维护不执行 VACUUM FULL。
- [River](https://github.com/riverqueue/river)：复用已有持久执行与终态 cleaner，不增加调度底座。
- [oasdiff](https://github.com/oasdiff/oasdiff)、[govulncheck](https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck)、[Gitleaks](https://github.com/gitleaks/gitleaks)：固定免费 CLI，分别检查 API 兼容性、可达漏洞及密钥；工具失败属于未验证。
- 复用已有免费腾讯云监控；不能核验的外部主机/网络覆盖列为缺口。

## 实施与验收

并行交付巡查、生命周期、CI；共享 Composition、EER、jobqueue 和页面由集成人统一合并。每一轮验证绑定准确 HEAD/tree；专项测试、完整 CI、部署版本、业务读回、Provider 接受和群内可见分别留证。

必须验证：29/30/31 天边界，业务与防重依据不变；清理后重放不能产生重复效果；并发使用对象受保护；中断恢复；当前与两个可用回滚版本保留；权限与 30 天在线读取限制；注入跨功能覆盖和错误清理规则被门禁阻断。24 小时验收需要真实形成 24 个唯一报告窗口及群内可见证据，不能由一次演示代替。

### 2026-09-18 实际证据与交付边界

本节记录已取得的证据，而非统一“验收通过”。编辑时集成 HEAD 为 `061a16d23cfaf468d409276357850e5a70b7ade6`、提交树为 `fc811df721826105f5040a0960328db2a7e4dde9`，另有进行中的未提交修正。下面各项只对其记载的源版本或输入摘要有效；之后的集成改动必须重新验证，不能继承早先分支的绿灯。临时证据路径位于本次执行机器；最终 CI artifact 需另行归档对应记录，不能假定这些本地路径可供所有维护者长期访问。

| 项目 | 已有证据 | 尚不能据此认定 |
| --- | --- | --- |
| 变更治理脚本 | `94a0bf73fb83ad68ad76d59b9f2dd65728876c63` / tree `15f94ef6679a2e68a1f1e66343a524c5d3969cee` 的干净树 fast 通过，证据 `/private/tmp/aicrm-governance-94a0bf7-fast/summary.json`。能力注册、真实消费者、保持性测试和错误规则回归已接入既有完整 lane | fast 不是全量回归；最终集成 HEAD 的完整 CI 尚需单独取得 |
| 诊断中间件 | `7d4a04f49a9dc5efadd4c15020d06a58a365fbdd` / tree `41c156ff383e9eb7bd4bc8483ef051eaf00c00ca` 的干净树 fast 通过，证据 `/private/tmp/aicrm-diagnostics-7d4a04f-fast/summary.json`；后续关联链集成和修正仍以最终版本专项为准 | 本地中间件验证不等于线上请求、River、EER 的完整关联链已经启用 |
| 前端执行工具升级 | `7acbed6453c7c10ebd236c97975137f70049768a` / tree `3ec31ad2cc7fc980254129dedaeb57e179af6165` 的干净树 fast 通过，证据 `/private/tmp/aicrm-npm-7acbed6-fast/summary.json`。正式 Orval 生成、生成物校验、根与 V3 typecheck、DOM 测试和构建已作本地专项；当前活跃工具与原冻结供体分开归属 | 本地 DOM、编译、构建和固定字节检查不是 Linux Host Chromium 或完整 CI；本次无旧供体外部 checkout 的验证不能写成 donor 全验证 |
| Go 安全修复 | x/net 已固定 v0.55.0；真实 govulncheck 原件 `/tmp/aicrm-governance-upgraded-vulns.json` 完整可解析且 `reachable={}`，SHA-256 `0026bca6d4c5f2bdeb0bc924984be5e70a40fe3480732f911179cd56162655a4` | 扫描原件没有内嵌精确 Git HEAD/tree，不证明最终集成源码、未知漏洞或生产二进制安全；最终 CI 需重新扫描 |
| npm 安全修复 | 上述 `7acbed6` 提交的增量和每周模式真实扫描：根项目 16 个受影响 package 条目归零，`web/v3` 为 0；随后显式增加 Swagger Parser 13.0.0 以保持 OpenAPI 校验入口可运行；该锁文件变更需最终集成扫描，不继承早先 hash 结论。原件 `/private/tmp/aicrm-npm-7acbed6-incremental/npm-security.json`、`/private/tmp/aicrm-npm-7acbed6-weekly/npm-security.json`，摘要见安全台账 | 每周模式本地运行不等于 GitHub 定时工作流已经在 main 运行；audit 不提供可达性或零未知漏洞保证 |
| 密钥扫描 | 对上述工具链提交实际运行固定 Gitleaks，新增 1 个 commit 无发现，原件 `/private/tmp/aicrm-npm-7acbed6-gitleaks.json` | 不是最终全部 commits 或完整 Git 历史的扫描；不能据此证明没有秘密 |
| 生产发布包盘点 | 仅在新CRM生产机运行只读 inventory，systemd 模板及 drop-in 已真实核验；本地证据 `/private/tmp/aicrm-release-inventory.zE7KA0/README.md`，远端临时证据 `/tmp/aicrm-release-inventory.OUmpQ0` | 没有 apply、删除、切换链接或重启；没有释放磁盘容量的成功 claim |
| 独立审核 | 2026-09-18 只读查询 main：严格 required `check` 已存在，但 required approving review count 为 0，CODEOWNERS/last-push review 均未要求 | 作者的当前 HEAD 声明不是独立批准。`independent_review=unverified`；按风险强制独立审核仍有仓库规则配置缺口 |

真实生产 inventory 的拒绝是安全保护结果，不能改写为“无异常”：当时 current 为 `cb274776148b8b300e05ff0ae4fb18bd57e6885c`，数据库 migration 178 条、最高 `0184`，当前 SQL 文件的 version/name/checksum 与只读快照逐项一致。但 current 含 480 个未登记的 macOS `._*` sidecar，严格产物所有权校验失败；165 个 release 条目中，独立保守盘点得到 79 个完整包通过校验，却没有任何一个同时满足已安装 schema 一致的回滚候选。inventory 因 `current_package_or_installed_schema_unverified` 拒绝。`plan-v2.json` 是失败后空文件，禁止作为 apply 计划；`blocked-plan.json` 只是所有条目受保护、候选 0、候选字节 0 的诊断记录。通过完整包校验的 14,099,388,641 字节并非可删除量。备份始终未进入候选。

最初进程引用查询观察到 current 和旧 `142d58a183f64e4c82f177c1cabdb2cb657d647d`；之后仅可执行文件查询未再发现旧版本，不能推导 cwd、fd 或其他引用全部消失。下一次正式计划仍需新鲜 schema、服务与进程引用证据，以及两个完整且 schema 兼容的回滚包；“保留当前加两个”目前是执行保护约束，尚未达到生产可清理条件。共同安装锁只协调遵守该锁的发布/维护进程，不保证任意不合作进程在最终引用检查后不会打开旧文件。

生产只读容量盘点支持首期优先治理发布产物、按Owner小批清理数据库的选择，无需立即大改业务表或部署分区扩展。具体主机地址、容量原件和主机配置核验记录保留在受控运维证据中，不进入公开源码。备份建设、验证和清理不在本次执行范围。

2026-09-18补充群内只读核验：已在原群实际看到原机器人的“系统运营巡检小时报”历史；群名及消息内容仅保留在本地验收记录。原系统唯一enabled且valid的Webhook与新机固定凭据逐字节核对，root 0600的目标证明只保存凭据摘要。该核验未发送消息、未启用治理；新V3报告的Provider回执及群内可见仍须部署后分别验证。

最终交付仍须依次取得：最终干净提交的精确 HEAD/tree 与完整 GitHub Linux CI；已部署版本及迁移读回；管理员鉴权与真实页面/后台链验证；飞书 Provider 接受及目标群内可见回执；至少连续 24 个唯一小时窗口的真实验收。业务故障仍只报不自动修复，`outcome_unknown` 不盲目重发。生产权限、配置或外部资源不可核验时必须显示缺口，不以本地测试或 HTTP 202 代替实际完成。

## 明确不在范围

备份建设、恢复目标及备份清理；自动修复身份或资金；新增收费服务；引入 Redis、Kafka、Kubernetes 或另一套队列与外部效果内核。
