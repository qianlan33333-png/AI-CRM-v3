# PRD-06 AI 助手计划闭环

状态：批准开发；先读 00-control.md；Terra high；沿用“AI助手”。

## 1. 基线与分类

沿用 `docs/14-PRD-AI助手-OneID与持久审批执行.md`。优先复用现有 aiassistant/port、intake、审批及 outbound private-message 实现。供体为旧版 cloud_orchestrator 的 review_plans 和计划一级/二级页面。

OneID：仅 Resolve/读取 canonical 客户，不建客、关联或合并。持久化：本地 UoW、River 内部执行、必要 Provider read、outbound 写效果。

## 2. 当前差异

计划、内容、版本、审阅、整单审批、机器 intake 和发送接口已有实现；生产 intake/dispatch 未启用且无运行样本。需要用本地真实调用方协议、PG 与 Provider 模拟验证闭环，不能另造 AI 平台来填充数据。

## 3. 用户流程

1. 现有外部调用方通过已认证机器 intake 提交计划、目标、内容和来源键，重复同载荷返回原计划，漂移冲突。
2. 计划列表保持旧统计、搜索、筛选、空态；详情保持人员分页、抽屉、内容卡片、素材预览和编辑。
3. 逐人通过/驳回只保存审阅，不发送。整单确认是唯一发送闸门；版本改变必须重新确认。
4. 审批冻结客户、发送人、资格、内容和素材；同 UoW 接纳 outbound intent、效果、任务、绑定与审计。
5. 发送前复核当前可信目标与资格，变化导致安全阻断而不是沿旧审批发给新归属。
6. 执行记录区分 accepted/queued/attempted/provider accepted/unknown/reconciled；没有送达证据不显示已送达。

## 4. 契约与复用

复用 `/api/integrations/ai-assistant/review-plans` 和现有 admin plan API、签名/nonce/重放保护。若旧调用方 DTO 与 V3 不同，在边缘 Adapter 最小转换；不得以裸外部 ID 或手机号兜底新建客户。

aiassistant 独占计划、内容、决定和绑定；各外部身份、发送人、素材经对应稳定 Port。前端 donor byte-frozen，只改 Host。保持既有 5000 人/20 步等已批准限制，不扩大成 Campaign 或通用 Agent 平台。

本轮不导入旧 AI 历史执行，不重放旧发送；没有历史数据迁移不是本板块遗漏。

## 5. 验收

- 模拟真实调用方签名 intake→列表→详情编辑→逐人审阅→整单审批→本地 Provider→回执完整 journey。
- 1/50/51/5000 人分页、重复来源、错误签名/过期 nonce/重放、目标缺失/冲突/错误 scope。
- PG 审批 CAS 和原子回滚，整单确认重放、逐人批准零发送、重启恢复、unknown 不换键。
- 不可发送目标不能被悄悄丢掉后宣称全量成功，返回 disposition 与安全原因。
- frozen UI 与错误/加载态对照；输出未合并 PR、测试证据和生产 intake/dispatch 配置待办。

## 6. 非目标

不新增模型内容生成、聊天摘要、全局 MCP、Campaign、历史执行迁移或观测平台。会话存档读取若旧调用合同确有依赖，仅接已存在窄只读 Port，不复制存档实现。

## 7. 总控预审定位

- d6 的 `http/handler.go:integrationPlan` 将已签名请求体中的身份直接标记为 AssuranceVerified；机器请求验签不能替代 Provider 身份验证。迁移时通过既有 Identity 查询边界匹配已有可信身份，输入本身不得升级、附着或建客；审批仍是唯一发送闸门。先核对现有 Resolver 与可信读取 Port，最小修正，不另造身份验证服务。
- `CreatePlanFromIdentities` 目前用包含 Nonce、OccurredAt、ExpiresAt 的整条 command 作为业务幂等摘要。需真实测试：相同来源和业务键、同内容，用新的合法签名时间及 nonce 重试应返回原计划；改内容应冲突。认证重放收据与业务内容摘要分别核验，不能为了重试去关闭 nonce 校验。
- 现有身份解析只对成功目标建计划并返回汇总计数；未解析目标不能在结果里静默消失。核对旧调用方契约，提供不含裸身份的逐项目标序号/安全状态结果；零可用目标也有明确可回读的结果。沿用已有业务收据存储，不扩展治理台。
- 旧供体实际入口为 `growth/cloud_orchestrator/review_plans.py` 与 api.py 的 `/api/admin/ai-assist/review-plans`；需要冻结其单人输入及批量包装到V3边缘的兼容映射，而非只验证自造的V3请求。
