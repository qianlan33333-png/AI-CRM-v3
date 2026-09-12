# PRD 01：客户负责人迁移与交接

状态：批准开发；总控 00-control.md 为共同约束。

## 来源与差异

固定旧源见总控：aicrm_next/crm/owner_migration/{api,application,repo}.py、app/admin_console/templates/admin_console/owner_migration.html，及 channels/integration_gateway/wecom_channel_entry_client.py 的协议。application 的 local_only、wecom_then_crm、transfer_result 为基线。

V3 已有 web/src/admin/templates/ownerMig.html、ownerReassignmentFile.ts、web/src/api/admin.ts 预览/确认/导出及 /api/v1/contact-owner-reassignments/* 客户端，缺真实服务/路由。migrations 0022/0086 是 WeCom 观察，不能当本地交接表。实施前冻结行为对照和 Journey；旧页面/现有解析器优先复用，Host 必要差异只为模式、员工选择和真实结果。

## 流程和业务规则

1. 授权管理员选存在的源员工和有效目标员工，范围全部符合客户或 Excel/CSV 名单，下载模板、校验格式/大小、逐行列错。已有 CSV API 兼容；Excel 复用本地解析器。
2. 选择 local_only 或 wecom_then_crm，保留可选企微转接及转接提示语。预览列候选/排除/冲突，冻结 customer ID、staff ID、关系版本、范围/hash、模式和提示语；有效 30 分钟。
3. 确认带预览 ID/hash、确认语、幂等键。重验权限、目标有效、关系版本/范围；变化显式冲突，不扩大范围。重复同请求返回同批次，不同参数拒绝。大批 River 分段。
4. local_only：只更新本地，企微 skipped/local_only，零 Provider。
5. wecom_then_crm：先企微（每批最多100），仅明确逐客户成功更新本地。失败/缺行/未知不更新；HTTP成功不是每行成功。受理、回查结果、最终观察分开。
6. 批次/逐行进度、错误和结果导出、原转接结果查询完整。重复确认/重启/回查不再转接；unknown只原标识查询或对账。

## OneID、Owner 和事务

分类：canonical客户读取/兼容身份解析，不建客；本地UoW、River、Provider读写。

Customer 拥有本地负责人（customer_id、staff_id、version、来源）及预览/批次/明细/收据。显式本地负责人优先显示；无显式记录时只读 fallback 可信 WeCom 主负责人，标明来源。WeCom 同步不得覆盖本地关系；保留独立企微跟进员工观察。

internal/customer/port/owner_handoff.go 提供 Preview/Confirm/Get/List/Report/OwnerRead，类型按现有规范细化。Access Port 映射 local staff 到 scoped userid；外部客户编号必须 Identity Resolve，pending/conflict 禁止交接。

outbound 拥有 transfer_customer 写意图；WeCom 拥有 transfer_result 读及关系观察。效果标识=交接批次+稳定协议子批，四摘要首次冻结，业务表保存 effect_id，EER 不存 PII。

local_only 分段 UoW 同时 CAS关系/结果/收据/审计/Outbox。Provider 模式同事务意图/收据/EER接受/任务，网络事务外。结果回写按本地版本 CAS；外部成功但本地版本变化需对账，不能覆盖新值或重复转接。

## 历史与不做

离线导入旧批次/行、模式、状态、时间，源ID幂等；历史终态不重发、不重新改负责人。客户/员工映射不明记录pending/conflict，数量守恒报告。不存在旧数据时用冻结fixture证明工具，不能伪称生产导入完成。

不做新离职继承产品、重新分配算法、建客合并、营销补发或新批处理框架。

## 验收编号

- O01 模板→范围→预览→确认→两模式→逐行结果/导出完整可操作，权限提示清楚。
- O02 local_only零Provider且后续同步不覆盖本地；观察和本地来源分开。
- O03 100+分段、部分失败/缺行仅成功行更新；请求字段和提示语符合旧协议。
- O04 过期/篡改/停用员工/关系版本冲突阻止；跨scope/未解析零效果。
- O05 真实PG任一步失败原子回滚、并发确认一批、重启恢复剩余行。
- O06 发送断连unknown不盲重试；查询零写；外部成功本地CAS冲突可对账。
- O07 历史导入重跑数量守恒/零新转接；页面受理不伪称成功。

交付源码、浏览器/协议/真实PG证据和PR准确HEAD，真实企微验收独立部署待办；不可只交骨架或本地模式。

补充旧行为：application.py:419-434只要求目标员工active，源员工须存在但可停用；本地交接不因源停用被拦。Provider模式按原转接协议的适用条件处理，禁止自动换成离职继承API。

受理与本地交接的精确语义：旧application.py:985-1056以transfer_customer逐客户明确errcode=0作为更新本地CRM的条件，不等待24小时最终接替。V3以经过精确客户/冻结摘要核验的Provider受理证据触发一次本地CAS，显示“本地已交接、企微已受理、最终接替待回查”。transfer_result和后续关系观察单独回读，不能把本地更新拖延到最终接替，也不能把最终观察伪称即时成功。

批量协议必须等价：旧 application.py:1014-1016 默认每100个客户一次transfer_customer。River每段100条但仍每客户一次Provider调用不构成这项恢复。冻结协议子批范围及逻辑效果ID，逐客户明确结果分别落账，丢行/未知不重发整个已可能执行的批次；不增加新执行框架。

## 冻结供体复用清单（本次确认收口）

旧仓 https://github.com/qianlan33333-png/AI-CRM，提交 `dd8d60dd8ddb983aca2ec88cc9e65a9f7563f79f`。下表与总控最新规则共同生效；已有V3实现优先复用，实际完成状态以验收矩阵当前HEAD为准。

| 分类 | 冻结依据/复用对象 | 收口要求 |
|---|---|---|
| 原样复用 | dd8d60d 的 owner_migration.html：原/目标负责人选择、话术、企微转接开关、全部/Excel范围、预览、确认、逐行结果和导出 | 当前 Host 是新的数字 ID/作用域表单，不能标为旧页面已复用；应接回旧字段和操作顺序 |
| Go 等价迁移 | crm/owner_migration 的预览冻结、两模式、100人子批转接、结果读取和历史；稳定身份/UoW通过V3适配 | 已有后端继续收口，不推翻批处理/历史成果 |
| V3 已有 | Access员工映射、OneID解析、jobqueue/EER/outbound、冻结前端文件解析器及现有客户入口 | 复用对应稳定 Port；作用域由可信配置提供 |
| 待补齐 | 当前准确HEAD真实Chrome失败、完整员工选择/文件范围旧旅程、执行后页面结果 | O01以实际本地负责人改变及Provider fixture结果回读验收，不能只检查accepted |
