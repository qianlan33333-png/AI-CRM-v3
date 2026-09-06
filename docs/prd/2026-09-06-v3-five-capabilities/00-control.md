# 五项旧能力恢复：总控交付契约

状态：2026-09-06 按用户最新指令，五个板块各自形成一个完整闭环 PR。整板块验收通过后可独立合并 main，不等待其他四项。停止向 #168 集成业务 HEAD；人工配置应用、历史生产导入及真实业务写入仍按独立验收边界处理。

## 固定基线

V3：50b86c8fbfa4974d3134748ecebbe02ef0d9f9ba；旧 AI-CRM：dd8d60dd8ddb983aca2ec88cc9e65a9f7563f79f。旧仓仅只读行为/页面/叶子协议供体，不能成为运行依赖。根任务只负责需求、协调、审核和合并验收；实现由 Terra high/xhigh、Luna max、Sol medium 完成，同时最多三条开发线。

依次生成 01-owner-handoff、02-customer-tags、05-runtime-config 并作为首批派发；有空位后派发 04-product-external-push、03-open-platform。每份 PRD 首次派发前必须完整。实施细节在已批边界内自主处理，新增产品范围或红线报根任务。

## 共同分类与边界

客户相关操作只读 canonical customers.id，兼容外部标识通过 scoped Identity Port 解析；本轮不自动建客、合并或升级可信度。配置、机器凭据自身不涉及 OneID。

业务事实、幂等收据、审计、Outbox、需要原子的 EER 接受必须同一个 PG UoW。内部任务用现有 jobqueue/River，Provider 网络在事务外。每张表唯一 Owner，跨域只 Port/版本化事件；企微写只有 outbound，观察归 WeCom。无新队列/lease/重试框架。

稳定逻辑效果 ID 不随配置变更；四摘要冻结校验。accepted/queued/attempted/executed/outcome_unknown/reconciled 区分，未知结果不换 key 重试。Provider 默认 disabled，密钥/PII 不入日志/文档/EER。

## 共享修改协调

Customer 两条线分别用 owner_handoff_*、tag_command_* 新文件和独立表；共同文件必要修改在 PR 交接表列明。Customer 本地负责人不覆盖 WeCom 观察；Tag 目录归 Tag；Access 是 staff 映射/权限唯一来源。Config 稳定快照 Port 由 Composition 注入，platform 不反向依赖 Config。Order 拥有原生首次 paid 事件，Product 拥有配置，outbound 拥有外推业务投递事实。

Composition/OpenAPI/生成客户端/权限清单允许提交真实装配的必要改动，由各板块对齐最新 main 处理冲突，根任务审核准确 HEAD，不能为避冲突省略装配。

迁移预留：负责人 0092、标签 0093、配置 0094、外推 0095、开放平台 0096；每项优先一份 additive migration。额外编号由根分配。不得复制他人占位迁移；若 main 并发增加由根任务协调调整未部署编号，禁止改上线迁移。

## 开发与完成条件

独立 clean clone 从固定基线开始，分支 codex/；不得夹带根目录未提交内容。PR 指向 main，合并前对齐最新 main 并复核准确 HEAD。每个板块只有一个完整闭环业务 PR，不能以接口骨架或局部后端交付代替整板块验收。审核通过即可独立合并，不等待其他板块；不再向 #168 纳入业务 HEAD。

每项必须有旧行为表（来源、已完成/待修复/待迁移）、前端复用及必要差异、真实路由装配、真实 PG 并发/回滚/重启测试、权限/OneID/协议/未知结果用例、浏览器 Journey 及历史导入对账。历史导入离线执行，源 ID+批次幂等映射；pending/conflict 明示，终态不产生新效果；fixture 验证不称生产导入完成。

最终交付五 PRD、实现 PR、验收矩阵、独立生产待办。绿色 CI、HTTP200、菜单或 accepted 不代表业务完成。真实 Provider 与测试 Provider 证据分开。

## 当前 PR 与调度

| 板块 | 唯一业务 PR | 调度 |
|---|---|---|
| 负责人迁移 | #171 | Terra xhigh，20,000 条本地恢复已验证，继续真实100客户Provider批次与完整旅程收口 |
| 通用客户标签 | #169 | Terra high，继续观察并发、历史和完整验收 |
| 配置中心运行生效 | #170 已合并 | 完整CI/根审核通过，main 5494537；执行者继续商品外推 |
| 商品／订单外推 | 新建一个完整板块 PR | 配置执行者已接续开发 |
| 通用开放平台 | 新建一个完整板块 PR | Terra xhigh 已派发；先完成标签真实重启补测后继续 |

上述每个 PR 都须包含本板块必要的后端、最小前端适配、迁移、历史导入、真实 PostgreSQL、并发与恢复、浏览器与协议测试及完整运行装配。独立合并不得依赖其他尚未合并板块的迁移或装配。允许分步提交，不能拆成待后续补齐的业务 PR。

#167 等共用基础缺陷修复保持独立小 PR，准确 HEAD 审核通过后独立处理；各板块复用相同提交，避免复制实现。#168 只保留阶段记录并关闭，不再是交付或合并门槛。#166 只承载 PRD 与协调记录。main 的既有自动部署必须单独记录实际结果；PR 合并不等于生产配置已发布或真实 Provider 验收已完成。

独立迁移顺序补充：现有迁移器逐版本查账本，允许0094先上线后补0092/0093。涉及EER约束的0092、0093、0095必须各自保留所有既有kind及本轮冻结的customer_owner_handoff、customer_tag_command、commerce_product_push三个outbound kind，不能由后到的低号迁移收窄已上线约束。各模块须验证逆序应用与既有事实兼容；不依赖其他尚未上线板块的表。
