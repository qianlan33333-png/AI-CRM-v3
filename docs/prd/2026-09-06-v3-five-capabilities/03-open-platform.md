# PRD 03：V3 原生最小开放平台

状态：2026-09-06 产品范围已确认，替代本目录此前“兼容旧版 56 条机器接口”的交付口径。沿用 PR #173 已完成成果，在同一个板块 PR 内收口；不推倒重来。

## 目标

为外部系统与 Agent 提供一个可安全上线、可持续扩展的 V3 原生入口：调用方登记与授权、短期 Token、能力发现、客户解析与上下文读取、客户活动读取、AI 审阅计划创建、异步结果查询，以及 MCP 工具发现和调用。

REST 与 MCP 是同一组应用操作的两种传输方式，必须调用相同的领域 Port、权限判定与审计链。禁止分别实现两套业务逻辑。

本轮不兼容旧仓 56 条 method/path，不迁旧路径、旧响应字段、旧 Token 或旧 Direct API Key 协议；未纳入 V1 能力目录的旧路径不得挂载占位路由。旧路由清单只保留为历史证据，见 `03a-machine-route-inventory.md`。

## 最高优先级分类

- **OneID：涉及。** 所有客户解析和读取以 `customers.id` 为唯一业务主键。外部身份必须携带 kind、scope、value；只调用 Identity Port 的可信解析，不隐式建客、绑定、合并或升级 assurance。pending、not_found、conflict 必须如实返回。
- **持久化：涉及。** Access 拥有 API Client、credential digest、auth version、grant 与审计；管理状态、版本和审计在同一个 PostgreSQL UoW 提交。业务读取只能通过对应领域 Owner Port，不跨域读表。
- **内部持久任务：仅复用。** AI 审阅计划进入现有 AI Assistant 审批/任务链；本模块不创建队列、Worker、lease、重试或对账框架。
- **外部效果：本轮不直接执行。** 开放平台只创建审阅计划或业务意图，不直接发送企微消息、不直接群发。后续效果仍由原领域审批、outbound 与 External Effects 链执行并回读状态。

## V3 已有能力与复用策略

旧仓固定只读供体为 `qianlan33333-png/AI-CRM@dd8d60dd8ddb983aca2ec88cc9e65a9f7563f79f`，仅用于理解安全边界和业务语义，不再作为接口兼容基线。

| 分类 | 复用内容 | 本轮处理 |
|---|---|---|
| V3 已有，直接保留 | API Client 管理、grant、一次性 secret、轮换/停用、OAuth2 client_credentials、audience/scope/capability/CIDR、每次调用的 enabled/expiry/auth_version 校验 | 补齐 V1 能力授权和完整运行装配，不重写授权核心 |
| V3 已有，直接保留 | MCP JSON-RPC、initialize、tools/list、tools/call、OneID scoped resolve、PostgreSQL 审计/历史导入基础 | 统一接入下述 Operation Catalog |
| Go 等价接入 | Customer、Identity、Archive、Survey、Radar、Order、AI Assistant 已有稳定 Port | Host 只做认证、DTO、授权和组合；不跨域访问 store/app/http/provider |
| 停止迁移 | 旧版 56 条路由、旧字段兼容、旧 audience/package/simple/e2e 等专用机器入口 | 不再作为 #173 验收条件；未完成路径不挂载 |
| 历史凭据 | 已完成的旧调用方/授权/审计提取与停用导入 | 可保留审计事实；默认 disabled/reissue_required，不恢复旧 secret/token，不阻塞 V1 上线 |

已通过审查的增量迁移和共享基础不回滚。只为旧响应兼容存在、且尚未形成稳定 V3 契约的投影不得成为上线依赖；若保留会扩大攻击面或长期维护面，应停止挂载并在 #173 中删除。

## 统一 Operation Catalog

V1 只承诺以下应用操作。能力目录按当前真实装配返回；领域能力未就绪时不得通过空数组、伪 200 或固定 `accepted` 冒充可用。

| operation_id | REST | MCP 工具 | 权限能力 | 结果 |
|---|---|---|---|---|
| `platform.capabilities.list` | `GET /open/v1/capabilities` | `list_capabilities` | `platform.capabilities.read` | 当前调用方可用操作、活动类型和 schema 版本 |
| `customer.resolve` | `POST /open/v1/customers:resolve` | `resolve_customer` | `customer.resolve` | canonical customer 或 pending/not_found/conflict |
| `customer.context.get` | `GET /open/v1/customers/{customer_id}` | `get_customer_context` | `customer.read` | 经授权的数据范围内客户上下文 |
| `customer.activities.list` | `GET /open/v1/customers/{customer_id}/activities` | `list_customer_activities` | `customer.activity.read` | 分页的 message/survey/radar/order 类型化活动 |
| `ai.review_plan.create` | `POST /open/v1/ai/review-plans` | `create_ai_review_plan` | `ai.review_plan.create` | 审阅计划 ID 与当前审批状态 |
| `operation.get` | `GET /open/v1/operations/{operation_id}` | `get_operation_status` | `operation.read` | accepted/queued/attempted/executed/outcome_unknown/reconciled |

认证端点沿用 `POST /oauth/token`；MCP 沿用 `GET /mcp` 与 `POST /mcp`。路径和 operation_id 一经发布即按版本化契约维护。

### 通用协议

- JSON 响应使用稳定 envelope：`data`、`error`、`request_id`；错误至少区分 authentication、permission、validation、not_found、identity_pending、identity_conflict、rate_limited、dependency_unavailable、outcome_unknown。
- 列表默认 50 条、最大 100 条，使用不透明 cursor。调用方不得提交数据库 offset、SQL、表名或任意筛选表达式。
- 写入要求 `Idempotency-Key`。同一调用方、同一操作、同一 key、同一请求摘要返回原 receipt；请求摘要不同则冲突拒绝。
- `request_id`、`operation_id` 和审计 actor 可关联，但响应与日志不得泄露 secret、Token、openid、external_userid、手机号或未授权 PII。
- 内容类型、body 大小、分页和超时均设上限；生产只允许 TLS，并对 Token 与凭据响应设置 `Cache-Control: no-store`。

## 客户解析与上下文

`customers:resolve` 接收一个或多个带作用域的外部身份引用。HTTP 请求不能自报 `verified`；只有内部已验证 Adapter 可以构造可信 identity。多个可信证据落到不同 Customer 时返回 conflict，不能择一绑定。

客户详情和活动读取在进入各领域查询前完成调用方的数据范围校验。禁止先读全量再在 Host 内过滤，也不得通过猜测 `customer_id`、订单号或活动游标绕过 owner 范围。

活动流由 Host 通过稳定 Owner Port 组合，支持 `types=message,survey,radar,order`。每项包含 `activity_id`、`type`、`occurred_at`、`source` 和该类型受控 payload；不制造跨领域公共表。只在对应 Owner Port 已真实装配、授权和测试通过时将该类型发布到 capability catalog。

## AI 审阅计划

外部调用只能创建待审阅计划，载荷沿用 AI Assistant 已有计划输入和审批规则。调用方不能在 payload 中指定管理员、伪造审批通过或直接触发 Provider 写入。

创建计划与幂等收据、审计在原领域 PostgreSQL UoW 中原子提交。计划后续执行继续使用既有持久任务和 External Effects；开放平台通过 `operation.get` 回读真实状态。`outcome_unknown` 不换幂等键盲重试。

群广播、自动化计划发布和直接企微发送不属于本轮最小平台。未来新增时先在领域内形成稳定命令，再向 Operation Catalog 增加版本化操作。

## 调用方管理与授权

管理员页面保留 V3 已完成的调用方创建、一次展示 secret、轮换、停用/吊销、到期、Token TTL、CIDR、grant 和审计。OAuth2 client_credentials 是 V1 唯一对外凭据模式；不引入 refresh token 或授权码模式。

请求 scope 只能比客户端 grant 更窄。每次 API/MCP 调用都校验 client enabled、expiry、auth_version、audience、scope、capability 和可信代理解析后的来源 CIDR。机器 actor 固定为 `machine:<client_id>`，不得转换成虚假管理员 ID；领域仍只接受人工 actor 时，通过该领域 Owner 的最小 actor_kind/actor_ref Port 扩展。

建议继续使用现有 `external_integration` audience，减少配置改动；V1 权限以表中细粒度 capability 为准。Direct API Key 已有实现如删除风险较大可留在代码中，但不得在新页面、能力目录和验收中作为推荐或必需入口。

## MCP 约束

MCP 协议继续支持 initialize、tools/list、tools/call，工具列表根据同一调用方 grant 动态裁剪。每个工具只适配 Operation Catalog，不直接引用领域 store/app/http/provider。

REST 与 MCP 对同一 operation 必须产生相同权限结果、OneID 结果、幂等 receipt、审计 actor 和业务状态。MCP 输入 schema 与 REST DTO 可以采用不同传输形态，但不得出现能力或业务语义分叉。

## 管理前端与 PR #164

新前端壳只需接入调用方列表/详情、创建、一次性 secret、轮换、停用、grant 编辑、最近调用/审计，以及 V1 capability catalog。删除“旧接口目录完整兼容”的页面要求；接口文档展示 V1 Operation Catalog、权限、请求示例、错误和当前真实可用状态。

前端不得缓存或再次显示 secret，不允许通过 UI 配出超出服务器白名单的 capability、audience 或任意 secret 引用。

## 历史与迁移

开放平台迁移继续使用已分配的 0096 及经协调的后续 additive migration。旧 client、grant 与 audit 可离线导入为来源事实；不可验证凭据始终 disabled/reissue_required。相同源行跨批次幂等，旧本地客户数字 ID 必须经过可信映射；未映射或冲突不能因轮换凭据而获得数据范围。

不导入历史 Token，不恢复旧 secret，不把旧 Direct API Key 转换成可用 OAuth 凭据。新 V1 调用方由管理员重新创建或明确重颁。

## 非功能要求和失败方式

- 授权撤销或 secret 轮换后，旧 Token 在下一次调用立即失效。
- Identity pending/conflict、数据范围拒绝、领域 Port 不可用和 outcome_unknown 都必须保持可区分，不降级成空成功。
- 审计写失败时，管理写和计划创建整体回滚；Provider 网络调用不得持有数据库事务。
- capability catalog 只声明当前 Composition Root 已注册且通过契约测试的操作。
- 每个领域故障被隔离：某一活动 Owner 不可用时返回明确部分失败或依赖错误，不推进虚假游标。

## #173 收口顺序

1. 以当前 #173 准确 HEAD 为起点，先删除旧 56 路由的剩余派工和验收引用，盘点已完成代码为“直接复用／改接 V1／停止挂载”；不得回滚已验证的 Access、MCP、历史和机器 actor 基础。
2. 冻结 6 个 operation_id、REST DTO、MCP schema、错误、capability、数据范围和幂等契约，生成一份共享 Operation Catalog；之后前后端和测试都从该目录读取或校验。
3. 先完成调用方管理、OAuth 与 `platform.capabilities.list`，以真实授权证明控制面可用；再完成 `customer.resolve` 与 `customer.context.get`，锁定 OneID 和 owner scope。
4. 将已存在的 chat、survey、radar、order 查询适配到 `customer.activities.list`。每种类型独立通过 Owner Port 契约后才登记可用，不等待未就绪的历史兼容接口。
5. 接入 `ai.review_plan.create` 和 `operation.get`，验证机器 actor、幂等、审批与重启恢复；不增加直接发送入口。
6. PR #164 接入管理页与 capability catalog，跑真实 PostgreSQL、REST/MCP 协议、Chromium 和全量 CI。只修新范围的具体失败，不再补旧路径测试。

第 2 至第 5 步可以在 #173 内按提交分段审核，但最终仍以一个完整板块 PR 合并。message、survey、radar、order 四种活动均须通过对应 Owner Port；发现缺 Port 时补最小稳定读取 Port，不得跨域读表换取表面完成。

## 验收标准

1. **授权闭环：** 管理员创建调用方、一次取 secret、取 Token、调用授权 API/MCP、轮换/停用后旧 secret 与旧 Token 立即拒绝；覆盖到期、CIDR、audience、scope、capability 和可信代理伪造。
2. **双传输同义：** 上述 6 个应用操作通过 REST 与 MCP 使用同一处理器；成功结果、错误、权限、审计和幂等行为一致。
3. **OneID 边界：** scoped identity 正常解析；缺 scope、pending、conflict 不误绑、不建客，越权 customer_id 被拒绝。
4. **真实数据读取：** 在真实 PostgreSQL 中验证客户上下文与已发布活动类型的分页、cursor、owner 范围、PII 字段授权和依赖失败；没有数据的真实空结果与能力未装配可区分。
5. **AI 计划闭环：** 重复请求只创建一个待审阅计划；机器 actor 可追溯，不能绕审批直接发送；任务重启后 `operation.get` 回读真实状态，unknown 不盲重试。
6. **管理与历史：** 管理 UI/API、审计、并发轮换/停用、事务回滚和历史导入对账在真实 PostgreSQL 通过；历史导入不激活旧凭据。
7. **新壳 Journey：** PR #164 页面完成创建、授权、轮换、停用、能力目录和一次真实只读调用；浏览器不得依赖旧 56 路由。
8. **移除伪兼容：** 未纳入 V1 的旧路径不挂载，返回标准 404；代码、OpenAPI、前端和测试不存在 501、固定 accepted 或空工具冒充完成。

## 完成定义

PR #173 在一个准确 HEAD 中交付上述后端、管理前端适配、数据库迁移/历史工具、Composition Root 装配、OpenAPI/MCP schema、真实 PostgreSQL、并发/恢复、协议和 Chromium 证据。完整 CI 全绿后方可整板块审核。

“旧版 56 路由等价迁移”及其兼容测试自本口径起不再是阻塞项。任何新增操作必须先说明领域 Owner、OneID/持久化/外部效果分类和授权能力，再独立扩展 Operation Catalog。
