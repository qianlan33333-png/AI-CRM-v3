# ADR 0010：采用 V3 原生最小开放平台

## 状态

Accepted，2026-09-06。

## 背景

PR #173 已完成 API Client、OAuth2 client_credentials、授权、审计、MCP 和部分 OneID/业务 Port 接入，但原 PRD 把旧仓 56 条机器路由全部列为上线条件，其中多数尚未迁移。V3 当前没有必须继续使用这些旧路径的对外调用方；继续逐条适配会同时扩大接口面、测试矩阵和长期维护成本。

开放平台仍需满足两类消费者：普通外部系统偏好 REST，Agent 偏好 MCP。两者需要同一身份、授权、审计和业务语义。

## 决策

采用 V3 原生、版本化的最小 Operation Catalog，仅发布客户解析、客户上下文、客户活动、AI 审阅计划、异步状态和能力发现六个应用操作。REST 使用 `/open/v1`；MCP 保留 `/mcp`，两种传输适配同一应用处理器和领域 Port。

旧版 56 条 method/path 不再迁移或挂载。旧清单保留为历史证据，不作为 PR #173 的兼容与验收基线。OAuth2 client_credentials 是 V1 的标准凭据；旧凭据只可停用导入，不恢复 secret 或 Token。

OneID 只做带作用域的可信解析，不隐式建客、合并或升级可信度。开放平台不直接执行企微发送；AI 写操作只创建待审阅计划，后续复用原领域 UoW、持久任务、outbound 与 External Effects。

## 备选方案

### 方案 A：完整兼容旧版 56 条路由

可减少旧调用方改造，但当前没有需要兼容的已确认 V3 调用方。它要求长期维护旧参数、错误、权限模板和多个专用入口，也会延后整个板块上线。未采用。

### 方案 B：只发布 MCP

接口更少，但普通服务、调试工具和非 Agent 集成会被迫实现 MCP。也难以复用标准 HTTP 监控和 SDK。未采用。

### 方案 C：REST 与 MCP 各自实现业务逻辑

短期接线直观，随后会出现权限、幂等、错误和数据范围不一致。违反单一领域 Owner 与稳定 Port 边界。未采用。

## 结果

正向结果：上线范围从旧版 56 个兼容入口缩减为 6 个核心应用操作；REST 与 MCP 共享鉴权、审计、OneID 和领域逻辑；未来通过增加版本化 operation 扩展，不复制控制面和执行框架。

代价：旧调用方若未来重新启用，需要迁移到 `/open/v1` 或单独批准兼容 Adapter；活动聚合 Host 需要维护类型化投影和稳定 cursor；能力目录必须与实际 Composition Root 保持一致。

## 非功能约束

- 生产只允许 TLS；Token/secret 响应 no-store；secret/PII 不进入日志。
- OAuth 撤销、停用和轮换在下一次调用即时生效。
- 列表有分页上限，写操作有幂等键，接口有 body、速率和超时限制。
- 管理写、审计与幂等收据使用同一 PostgreSQL UoW；Provider 网络调用不持有事务。
- Identity pending/conflict、权限拒绝、依赖不可用和 outcome_unknown 不得转为空成功。
- 领域通过稳定 Port 接入；开放平台不得跨域读写表或 import 领域 app/store/http/worker/provider。

## 验证

按 `docs/prd/2026-09-06-v3-five-capabilities/03-open-platform.md` 的八项验收执行，重点验证 REST/MCP 同义、OneID 冲突不误绑、机器 actor、真实 PostgreSQL 并发/回滚、AI 审批不被绕过，以及旧路径未挂载。
