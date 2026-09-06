# 五项验收与审核矩阵

基线50b86c8，更新2026-09-06。只按证据更新，不把计划/派发当完成。

| 板块 | PRD | 当前实现证据 | 完整板块 PR / 最新已知 HEAD | 根审核与剩余项 |
|---|---|---|---|---|
| 负责人迁移 | 01 已批准 | 两模式、真实 River/Provider fixture、并发互斥已有测试 | #171 `9aa5ac6380ce432d09803d0fdeafec1cebaf5d79` | 未通过整板块：20,000 条分段恢复、历史、完整浏览器旅程待补 |
| 通用客户标签 | 02 已批准 | 根独立 PG/race 通过74f560e；后续网关unknown、Channel冲突及观察刷新已修 | #169 `1d7a71d813ebf1abe3c9a0b05db28b523678cf76` | 未通过整板块：观察与全量同步并发、历史导入、最终CI及复核待完成 |
| 配置中心运行生效 | 05 已批准 | 根独立 PG/race/HTTP/Host/history及installer测试通过 | #170 `c2906bb4f5f8eebb2a05848183bab00c9cb45022` | 业务审核通过；最后导入边界门禁修正待复核及完整CI |
| 商品／订单外推 | 04 已批准 | Terra xhigh 开发中 | 新建单一完整板块 PR，尚未提交 | 等待完整交付 |
| 通用开放平台 | 03及03a 已批准 | 冻结56条method/path；待标签执行者接续 | 新建单一完整板块 PR，尚未提交 | 等待完整交付 |

共用验收：完整路由/Composition、冻结供体复用、PG原子性/并发/重启、身份权限、未知结果、历史零新效果。各PR需链接实际日志/测试/浏览器证据，跳过与Mock明确标识。

## 独立交付与合并记录

按用户最新要求，整板块验收通过后独立合并；不再以总集成 PR 为交付单位。#168 停止接收业务 HEAD，只保留阶段记录并关闭。它此前只纳入文档及共用修复，没有五项业务实现需撤回。

- 共用修复 #167，准确 HEAD `cbac1f6bdb2b868985c577ba0cff11482430a19e`：完整 CI、根独立真实 PostgreSQL race 测试与审核通过。与五板块业务 PR 分开处理。
- 五项业务尚无整板块完成结论；单项证据通过不填为已完成。
- 每个板块合并前记录最终准确 HEAD、真实 PG/浏览器/协议/历史证据、CI及 review 结论；合并与既有部署流水线结果另外记录。
- 配置发布、凭据启用、真实转接/打标/外推、生产历史导入均未执行，不能由合并状态推导。

历史导入、真实消费者、浏览器操作、Provider协议及整体CI必须分别验收；局部通过不覆盖待办。

## 配置发布根审核证据

#170 业务提交f8fcc95与历史提交71f3bab已合入共享cbac1f6；根在独立review worktree对b1a8b5运行真实PG测试：Config Store/HTTP/Module/Webshell、历史CLIapply/replay/verify、cmd/aicrm自动化Provider全旅程race、旧任务config-observed数据库约束反例，均通过且无PG跳过。随后35ca888仅安装制品登记、ccc39cb仅README密钥输入说明；根对最终ccc39cb独立运行安装契约/缺失0094阻断测试通过。

消费者范围明确仅 `automation.operations.max_recipients_per_run`；旧键未确认等价的一律只读excluded/no_v3_runtime_equivalence，不声称已激活旧配置。生产配置发布与真实Provider仍未进行。

标签#169修正至74f560e：根独立PG/race通过。剩余网关无可信拒绝证据时unknown分类、Channel忙碌标签不回滚入客、只读观察刷新及历史导入由执行任务继续，尚未批准整板块。
