# 五项旧能力恢复：总控交付契约

状态：2026-09-06 用户确认计划，批准 PRD、开发、测试和 PR。本轮不含生产部署、真实业务写入或合并自动部署 main。

## 固定基线

V3：50b86c8fbfa4974d3134748ecebbe02ef0d9f9ba；旧 AI-CRM：dd8d60dd8ddb983aca2ec88cc9e65a9f7563f79f。旧仓仅只读行为/页面/叶子协议供体，不能成为运行依赖。根任务只负责需求、协调、审核、集成；实现由 Terra high/xhigh、Luna max、Sol medium 完成，同时最多三条开发线。

依次生成 01-owner-handoff、02-customer-tags、05-runtime-config 并作为首批派发；有空位后派发 04-product-external-push、03-open-platform。每份 PRD 首次派发前必须完整。实施细节在已批边界内自主处理，新增产品范围或红线报根任务。

## 共同分类与边界

客户相关操作只读 canonical customers.id，兼容外部标识通过 scoped Identity Port 解析；本轮不自动建客、合并或升级可信度。配置、机器凭据自身不涉及 OneID。

业务事实、幂等收据、审计、Outbox、需要原子的 EER 接受必须同一个 PG UoW。内部任务用现有 jobqueue/River，Provider 网络在事务外。表单 Owner，跨域只 Port/版本化事件；企微写只有 outbound，观察归 WeCom。无新队列/lease/重试框架。

稳定逻辑效果 ID 不随配置变更；四摘要冻结校验。accepted/queued/attempted/executed/outcome_unknown/reconciled 区分，未知结果不换 key 重试。Provider 默认 disabled，密钥/PII 不入日志/文档/EER。

## 共享修改协调

Customer 两条线分别用 owner_handoff_*、tag_command_* 新文件和独立表；共同文件必要修改在 PR 交接表列明。Customer 本地负责人不覆盖 WeCom 观察；Tag 目录归 Tag；Access 是 staff 映射/权限唯一来源。Config 稳定快照 Port 由 Composition 注入，platform 不反向依赖 Config。Order 拥有原生首次 paid 事件，Product 拥有配置，outbound 拥有外推业务投递事实。

Composition/OpenAPI/生成客户端/权限清单允许提交真实装配的必要改动，由根任务按审核准确 HEAD 集成处理冲突，不能为避冲突省略装配。

迁移预留：负责人 0092、标签 0093、配置 0094、外推 0095、开放平台 0096；每项优先一份 additive migration。额外编号由根分配。不得复制他人占位迁移；若 main 并发增加由集成统一调整未部署编号，禁止改上线迁移。

## 开发与完成条件

独立 clean clone 从固定基线开始，分支 codex/；不得夹带根目录未提交内容。PR 指向 main，但禁止合并；已审核 HEAD 可纳入无部署的集成分支。

每项必须有旧行为表（来源、已完成/待修复/待迁移）、前端复用及必要差异、真实路由装配、真实 PG 并发/回滚/重启测试、权限/OneID/协议/未知结果用例、浏览器 Journey 及历史导入对账。历史导入离线执行，源 ID+批次幂等映射；pending/conflict 明示，终态不产生新效果；fixture 验证不称生产导入完成。

最终交付五 PRD、实现 PR、验收矩阵、独立生产待办。绿色 CI、HTTP200、菜单或 accepted 不代表业务完成。真实 Provider 与测试 Provider 证据分开。
