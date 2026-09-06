# PRD 05：业务配置发布与运行生效

状态：批准开发；遵循 00-control.md。

## 旧行为与现状

旧源固定 dd8d60d：aicrm_next/platform/admin_config/config_releases.py、config_release_repository.py、api.py 的 /api/admin/config/releases 和 admin/config/releases 页面；config_releases/new/detail 模板；platform/shared/runtime_settings.py、runtime_configuration.py。

旧链是草稿→校验→发布→活动版本/运行读取→回滚。runtime_setting 只对登记 managed/cutover 键消费已发布值，有环境兼容优先级，不是所有数据库值都覆盖环境。保留交互与版本/CAS语义，不照搬旧大表锁和SecretStore。

V3 internal/config/{port,app,store,http} 已有保存、校验、CAS/幂等、审计/Outbox；明确 local_only/runtime_applied=false。当前 releases 为AdminOps部署SHA观察，不是业务配置发布。cmd/aicrm从env一次装配，setting.updated无运行消费者。目录存在不算恢复。

## 范围与用户流程

1. 管理员在原配置中心编辑可管理业务键形成草稿，显示类型、当前有效值/来源和变更影响。提供预期基版本，不能覆盖别人刚发布的修改。
2. 校验键白名单、类型/范围、跨键约束和安全引用是否登记；展示可即时生效/仅新任务/需部署等真实作用范围。不受管的部署参数不可通过普通发布修改。
3. 确认发布同事务写不可变版本、active指针、幂等收据、审计/Outbox。并发发布只一个成功，冲突重新读取比较。
4. 业务在请求/任务明确边界获取有效快照并使用；管理端回读已保存、已发布、实际使用版本/时间/角色/来源，未观测到实际使用不能宣称全部runtime applied。
5. 回滚发布新版本引用旧内容，保留历史；回滚不撤回已经执行的业务效果。重启继续使用已发布版本。

## 第一批真实消费者与键清单

必须首先接通当前已经存在的 automation.operations.max_recipients_per_run（与 cfg.AutomationOperations.MaxRecipientsPerRun 同语义，键名可按既有规范选定一次后固定）。源是 cmd/aicrm/composition.go 当前自动化实际消费者；不能选 outbound.rate_per_second/max_attempts 这类尚无真实消费者的展示字段，再额外发明限流治理来凑生效。

有效值以配置已发布覆盖该业务环境默认；未发布保持现有默认/校验语义。API计划受理和Worker准备/执行均通过同一typed Port得到该值；业务明确选择任务冻结阈值还是当前值，并在验收中一致验证。推荐受理冻结业务计划限制，Worker验证同一冻结快照；新任务在新发布后使用新值，API/Worker记录相同revision。Provider停用/授权吊销仍按当前安全状态单独检查。

首期只登记上述真实参数，以及其他任务明确请求且已有消费者的受控目标安全引用/允许业务键；不因未来可能用就堆键。每个新增键记录Owner、类型/范围、默认、权限、消费者、角色、生效边界、是否敏感引用。执行者开始时交精确键表。

DB连接、监听、发布SHA、公共域名、CorpID/开放平台scope、加密主密钥继续部署管理，不得热改身份基础。现有local-only配置如corp/agent不能偷偷升级为运行事实；界面明确其状态或禁止普通业务发布。禁止任意文件路径/env变量引用。

## Owner、Port 与原子性

分类：配置本身不涉及OneID；本地事务和业务读取，无Provider写/专属定时任务。验证引用可通过受控读Adapter，不能自动启用真实Provider。

Config拥有草稿/不可变版本/active指针/校验与使用观测/发布收据。建议 internal/config/port/effective.go 定义 typed EffectiveSnapshot 和读取接口，包含revision、source、业务值；运行使用记录经独立稳定Port提交，不能每次热路径做重型日志。Composition注入业务，platform不得依赖Config。

使用观测可以记录已有业务命令/任务的配置revision，再由稳定领域Port回读；若Config记录角色最新使用，只轻量且有幂等/采样边界，不新建ticker/worker框架。单次操作读一个快照，禁止中途混版本。可缓存不可变revision内容，active校验策略须明确；不允许永久进程缓存造成多角色不一致。

敏感值只用安全引用，薄适配到V3已有受保护env/文件，严格登记允许的引用名。前端不显示原secret。Config发布持久化不写环境文件，不触发部署脚本，不迁回旧SecretStore。

## UI与兼容

复用旧发布列表、新建、差异/校验/详情、发布与回滚交互，采用Host最小适配。保留既有app-settings接口兼容，但响应如实标记saved/published/effective；部署release观察单独显示，不能混作配置发布记录。旧普通保存接口不自动发布扩大副作用。

## 历史导入

离线导入旧发布历史/键值映射，未知/部署键/未登记敏感引用列排除或pending，不静默丢失。旧发布记录默认历史只读，不能导入即激活；独立显式草稿可供后续部署审核发布。源版本幂等，重跑不创建新活动版本。fixture报告总数与每项结果。

## 验收

- C01 草稿→校验/差异→发布→有效回读→新任务使用→回滚完整页面流程，保存不冒充发布。
- C02 真实automation消费者：限制从A发布B，新请求和持久任务实际按B且revision一致；旧冻结任务语义明确，重启后仍B。
- C03 真实PG并发发布CAS、任一步失败active/审计/收据一起回滚，幂等键重放不新版本。
- C04 API/Worker两角色使用版本可证，未实际使用不标已生效；缓存不会永久陈旧。
- C05 未授权/非法值/任意路径/身份基础键被拒；Secret日志/响应无明文。
- C06 回滚为新版本、旧业务效果不撤销；历史导入幂等、不自动激活、不触发Provider。

迁移0094；交付真实消费者、UI/PG/Journey与准确PR HEAD。禁止只交发布表/接口，真实部署单独待办。

## 必要基线修复：并发 AI 回执聚合（2026-09-06）

文档PR166的基线CI与独立PG均复现：同一AI计划的两条effect completion并发时，旧聚合快照可将存在outcome_unknown的计划覆盖回dispatching。属于配置消费者联合回归的既有正确性缺陷，不是新功能。由当前Config执行者在AI Owner内提交独立修复commit：按既有一致锁顺序锁plan后更新/汇总recipient与binding，同一UoW保存；不得延长超时代替修复或把unknown视作成功。新增并发PG反例，原TestAudienceRefreshToAutomationProviderAndReadOnlyHistoryPostgreSQL必须通过。根已按独立准确HEAD审核并合并#167，共用修复继续与五板块PR分开记录；自动部署结果见验收矩阵。
