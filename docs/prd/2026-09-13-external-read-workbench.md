# v1.0 外部只读 API 与工作台接入实施 PRD

状态：审计冻结草案，待 Root 评审后派开发。  
日期：2026-09-13  
CRM 基线：`db884d26`，分支 `codex/external-read`  
交付范围：v3 CRM 侧只读合同、Catalog、授权、Owner Port 组合和验收用例。旧仓及旧服务器只作为行为和字段参考；工作台仓库、服务器和真实同步验收不属于当前已确认环境。

## 1. 目标与边界

为外部工作台提供一个短期令牌、细粒度授权、只读的 v3 Open Platform Client。v1.0 覆盖七项业务能力：

1. 客户身份与基础信息；
2. 订单列表；
3. 订单详情；
4. 问卷提交记录；
5. Chat 记录；
6. Radar 点击；
7. Radar 链接映射。

接口完成的定义是：真实 v3 数据可经 OneID 正确归属、所有目标能力可查询、Client 无写入权限、分页和撤销可验证、外部身份可追溯到手机号或带 scope 的 UnionID。HTTP 200、空列表、保存配置、Mock、单条测试或 CI 通过都不构成完成。

不在本期范围内：下单、支付、退款、发消息、建群、自动化、AI 写入、查询时调用 Provider、隐式建客、隐式绑定或合并、恢复旧 `/api/external/*` 路由、固定永久 API key、旧仓运行时依赖、通用同步/导出平台。

本功能涉及 OneID 和持久化读取，但不引入 Provider 读取、内部持久任务或外部效果。跨领域只使用稳定 Port；不得从 Open Platform 或新适配器直接访问其他 Owner 的表。

## 2. 当前审计结论

当前 v3 的原生 Catalog 在 `internal/openplatform/port/operations.go`，已有六项基线能力：`platform.capabilities.list`、`customer.resolve`、`customer.context.get`、`customer.activities.list`、`ai.review_plan.create`、`operation.get`。`internal/openplatform/http/handler.go` 的挂载层没有开放旧外部路由，旧 `/api/external/*` 请求会 404。

可以复用的稳定边界：

- `internal/identity/port` 的 Resolver、canonical lineage 和 verified identity 约束：负责解析，不隐式建客或合并。
- `internal/customer/port` 的 Sidebar、Owner、Tag、Survey、Timeline、Chat activity 投影：适合内部摘要和受控组合，但 Sidebar 目前只有掩码手机号等安全字段。
- `internal/order/port.Query` 的订单列表、详情、引用查询；`internal/payment/port.AdminQuery` 的支付、退款和订单 effect 只读查询；需增加外部安全组合投影。
- `internal/survey/port.ExternalSubmissionReader`：已有 native 与历史问卷读取和历史 UnionID 映射，但外部页目前是 offset 语义。
- `internal/messagearchive/port` 的 CustomerMessageReader 和 ExternalChatRecordReader：已有归档读取，需重新定义外部安全字段和游标。
- `internal/radar/port.ExternalLinkMappingReader`：链接映射已有 Owner Port、非删除过滤和 keyset 读取。

真实缺失：订单、问卷、Chat、Radar 点击、Radar 链接均没有面向 v1 的原生 Open Platform operation；Radar 没有专用外部 click reader；客户详情没有可安全扩大的外部明细投影。旧仓行为可以帮助冻结筛选和状态含义，不能复制旧 SQL、旧目录、旧身份主键或旧路由。

## 3. 七项能力矩阵

| 能力 | v1 目标 | v3 当前状态 | 可复用 Owner Port/行为 | 必须补齐的合同与数据缺口 |
|---|---|---|---|---|
| 客户身份/基础信息 | `POST /open/v1/customers:resolve`；版本化详情读取 | resolve/context 部分已有 | OneID Resolver、Customer/Sidebar profile | 原始身份可追溯；手机号、UnionID、备注、Owner、跟进员工按显式字段授权；保持 `customer.context` 兼容 |
| 订单列表 | `GET /open/v1/orders` | Order `List` 有，Open operation 无 | Order Query、Payment 查询、Customer identity | paid/refund 时间和状态过滤；统一金额/状态/身份投影；无客户订单保留；签名游标绑定筛选和授权 |
| 订单详情 | `GET /open/v1/orders/{order_id}` | Order `Get` 有，Open operation 无 | Order Query、Payment `AdminQuery`、退款读取 | 支付/退款/回调摘要/时间线安全组合；不得暴露签名、密钥、原始 effect 或 Provider 私密字段 |
| 问卷提交 | `GET /open/v1/questionnaire-submissions` | ExternalSubmissionReader 有 | Survey submission/history、历史 UnionID map | customer_id 必填；native/历史来源可区分；答案为提交快照；外部 opaque cursor；unavailable 不转空列表 |
| Chat 记录 | `GET /open/v1/chat-records` | Archive readers 有 | CustomerMessageReader、ExternalChatRecordReader | 私聊员工必须显式；群参与者受控；固定 20 条；媒体 unavailable；去重；禁止实时 Provider 读取和发送 |
| Radar 点击 | `GET /open/v1/radar-clicks` | 只有内部 Stats/Events 和 customer activity 摘要 | OneID、旧逻辑判定作为行为供体 | Radar 新建只读 click Port；只读可信授权/落地事件；pending/conflict 保留；排除匿名、IP、UA、OAuth 中间事件 |
| Radar 链接 | `GET /open/v1/radar-links` | ExternalLinkMappingReader 已有 | Radar link Owner Port、keyset cursor | Catalog/route/capability；返回稳定 id/code/title；禁用但未删除链接仍可查；不返回配置和凭据 |

## 4. 硬性身份可追溯合同

### 4.1 记录、身份和统一客户的三层关系

每个跨系统业务记录必须保留以下来源链路，不能只返回一个可能变化的展示字段：

| 层 | 必填/条件字段 | 规则 |
|---|---|---|
| 来源记录 | `source_system`、`source_record_id` | 保留真实来源系统和原始记录 ID；不同来源的 ID 不得混用；即使未匹配客户也必须保留 |
| 统一客户 | `customer_id`、`identity_status` | `customer_id` 只能来自统一 Customer/Identity Port；`unresolved`、`pending`、`conflict` 时为 null，不能猜测归属 |
| 外部身份 | `kind`、`scope`、`value`、`assurance`、`source` | value 是否返回由 field grant 决定；手机号和 UnionID 必须带身份 kind/scope，不能把 UnionID 当全局无 scope 主键 |

订单、问卷、Chat 和 Radar 点击的 DTO 必须有可审计的 `lineage` 或等价字段。对外输出可以只给授权后的身份投影，但 CRM 内部必须能从 `source_system + source_record_id` 回到原始记录；存在可信身份时再经 OneID 回到 canonical `customer_id`。Radar 链接映射是独立内容实体，只保留稳定 link ID 与 `source_system + source_record_id` 的 Owner 可追溯性，不附加 `customer_id` 或 OneID 链路。

### 4.2 客户关联记录的查询路径

以下统一路径仅适用于订单、问卷、Chat 和 Radar 点击；不允许各 Owner 自建匹配逻辑。Radar 链接是无客户归属的独立内容读取，只接受其稳定业务 ID/code 和授权范围，不调用 OneID：

1. Client 以 `customer_id` 或支持的身份引用查询。支持手机号、UnionID、OpenID、WeCom external_userid 等已登记 kind；UnionID、OpenID 必须带正确 scope。
2. Open Platform 先检查 token scope、operation capability、数据范围和字段 profile。
3. `internal/identity/port.Resolver.Resolve` 返回 `Found`、`NotFound` 或 `Conflict`；只有 `Found` 才可使用 canonical `customer_id` 查业务 Port。
4. 业务 Owner Port 以 canonical `customer_id` 查询本域记录，同时返回/组装 `source_system` 和 `source_record_id`。不得通过跨域 SQL 或旧仓 `person_id` 关联。
5. 如需向工作台回显手机号或 UnionID，Open Platform 再通过统一 Identity/Customer Port 读取授权字段，并在响应中标记 kind、scope、assurance；日志只保留脱敏值和审计引用。
6. 返回结果后，调用方可用响应中的 canonical `customer_id` 再查客户详情或其他业务列表，往返结果必须指向同一客户。

未匹配的历史记录仍可在允许的来源筛选下返回，但必须带 `identity_status=unresolved` 或 `pending`；冲突记录带 `identity_status=conflict`，不得自动选择一个客户。任何请求都不得隐式创建客户、绑定身份、自动合并或把 HTTP 空结果解释为没有历史数据。

### 4.3 手机号和 UnionID 的授权与精确查询

本轮专用 Client 可以查询原始手机号和带 scope 的 UnionID，但这必须是创建时明确记录的字段 profile/授权，不是因为拥有已有 `customer.read` 就自动获得。建议使用独立详情 operation（如 `customer.detail.get`）和能力（如 `customer.detail.read`），字段组至少拆分为：

- `identity.basic`：canonical `customer_id`、状态、匹配方式；
- `contact.phone.raw`：规范化手机号，需专用 Client 显式授权；
- `identity.unionid.raw`：UnionID 及其 open-platform scope，需专用 Client 显式授权；
- `identity.channel.raw`：OpenID/WeCom external_userid 等，按 channel scope 单独授权；
- `profile.remark`、`ownership`、`follow_employees`：分别授权，不因客户基本读取自动开放。

既有 Client 保持原 `customer.context.get` 摘要合同，不能因共享 `customer.read` capability 自动得到新敏感字段。详情投影可以采用版本化 DTO；若采用同一 operation，则必须要求显式 field grant 并拒绝未授权字段，不能用空字段或空 scope 默默放宽。

精确查询规则：

- `customer_id` 是唯一跨接口主键；手机号查询必须先按统一规范化规则经 Identity Port 查询，不能直接在业务表中按字符串猜测。
- UnionID 查询必须提供且校验 `scope`；没有正确 scope 时返回 `403` 或明确 `scope_required`，不能跨开放平台范围匹配。
- 唯一匹配返回 `Found`；无匹配返回 `NotFound`/`unresolved`；多个可信候选返回 `409 conflict`，不返回未经授权的候选明细。
- 请求体不能自报 `verified`；verified 证据只能由内部 Provider Adapter 构造。

### 4.4 覆盖率和缺失清单

上线前必须对每种有客户归属的业务记录统计真实覆盖率：总记录数、可回到 `customer_id` 的数量、仅有来源 ID 的数量、`pending`、`unresolved`、`conflict` 数量，以及按 `source_system` 和时间范围划分的缺失原因。响应或验收报告应能列出缺失记录的 `source_system + source_record_id`（原始身份值按权限脱敏）。

不能因为当前样本都能查到手机号/UnionID 就宣称全部历史记录 100% 完整；缺失数据必须作为可见状态和清单，等待明确补数或人工处理。数据修复仍需走 OneID 的显式流程，不能在本只读 API 查询时补绑定。

### 4.5 原有 ID、手机号和 UnionID 的可查询性

“保留身份链路”必须落成可执行的查询合同：订单、问卷、Chat 和 Radar 点击应支持 canonical `customer_id`、授权手机号、带 scope 的 UnionID 三种客户选择器；每个来源记录还应支持按 `source_system + source_record_id` 的精确回查。来源 ID 不允许只出现在响应里而不能作为查询条件。若一个 Owner 还有稳定的业务 ID（例如订单 merchant/provider reference、问卷 submission ID、Chat message ID、Radar event/link ID），应在对应列表/详情接口提供精确过滤，并以 `source_system` 防止跨系统碰撞。Radar 链接不接收任何客户选择器：它按 link ID/code 在完整已授权链接范围内读取。

这三种客户选择器必须走同一往返路径：

1. 用手机号或 `UnionID + scope` 解析到 `customer_id`；
2. 用该 `customer_id` 查询订单、问卷、Chat、Radar 等记录；
3. 从记录的 `source_system + source_record_id` 反查原始业务记录；
4. 再从记录返回的身份投影回查同一个 `customer_id`，并验证手机号/UnionID scope 与首次解析一致。

因此，“一定能查询”表示接口和授权合同必须覆盖这三类选择器，且对实际存在并有授权的身份给出稳定结果；不表示历史源数据凭空拥有不存在的手机号或 UnionID。源记录没有该身份、身份未同步或多身份冲突时，必须返回对应缺失/`unresolved`/`pending`/`conflict` 状态并进入覆盖率清单，不能伪造字段或宣称 100% 关联。

## 5. 授权、数据范围与游标

认证使用 `client_credentials`、短期 Bearer token、audience `external_integration` 和 read scope；Secret 只进 Secret Store，不进入仓库、日志、响应或工作台页面。`scope=write`、`external_write`、admin control-plane 一律拒绝。

建议的专用 Client capability 集合：`platform.capabilities.read`、`customer.resolve`、`customer.read`、`customer.detail.read`、`order.read`、`questionnaire.read`、`chat.read`、`radar.click.read`、`radar.link.read`。最终 operation/capability 名称需 Root 冻结；不要用 legacy `external_read` 代替细粒度检查。

当前 `internal/access/domain/machine.go` 的 `OwnerScope{}` 语义是 unrestricted，且创建输入无法区分“未选择范围”和“本次显式全量”。为新 Client 增加显式数据范围模式：

- `explicit_all`：本次明确选择全量，保存 profile、操作者、原因和审计记录；
- `selected`：明确选择一个或多个范围；
- `unconfigured`：未选择范围，禁止启用或读请求拒绝，不能解释成全量。

历史 Client 保持旧空 map 兼容语义，避免静默破坏；新 Client 不能因为空 map 继承全量。有效 scope、field profile 和 auth version 必须参与游标签名。撤销或授权变更后旧游标不得继续读取。

所有外部列表默认 opaque cursor；游标签名绑定筛选条件、effective grant、field profile、auth version 和必要的水位。失败不能推进游标，过滤条件、授权或数据水位变化不能复用旧游标。通用默认/最大 100，Chat 固定 20；时间输入按秒，输出 ISO8601；金额用整数分并附格式化元。

## 6. API 和字段附表骨架

以下是冻结前的最小字段骨架。字段名以 OpenAPI 最终稿为准，缺失值必须区分 null、空字符串、未授权和 unavailable。

### 6.1 客户

请求：`customer_id` 或 `{kind, scope, value}`，二者择一。  
响应：`customer_id`、`status`、`match_method`、`identity_status`、授权后的 `phone`、`unionid{value,scope}`、Owner/follow projection、remark、`identities[]`、`lineage`。不返回旧 `person_id` 或 map row。

### 6.2 订单列表/详情

请求：provider、created/paid 时间、product、payment status、`is_paid`、`is_refunded`、merchant/payment reference、customer_id、cursor、limit。  
列表响应：`order_id`、`source_system`、`source_record_id`、`customer_id`、`identity_status`、provider/reference、产品、created/paid 时间、状态及中文 label、amount/currency、paid/refund 状态。  
详情响应：列表字段、商品明细、支付摘要、退款记录、可退金额、回调摘要、时间线；禁止 callback body、签名、密钥和内部 effect 细节。

### 6.3 问卷

请求：`customer_id`、问卷标识、提交时间、cursor、limit。  
响应：`submission_id`、`questionnaire_id/title`、definition version、submitted_at、answers snapshot、labels、assessment snapshot、`source_system/source_record_id`、`customer_id/identity_status`。不在查询时重新评分。

### 6.4 Chat

请求：`customer_id`、时间范围、private/group scene、私聊 employee、cursor。  
响应：稳定 msg id、scene、message type、content/render、time、staff/group projection、authorized participants、media availability、source lineage。固定 20 条；媒体未归档时明确 unavailable。

### 6.5 Radar 点击/链接

点击请求的 `customer_id` 可选：受限 Client 指定客户时只读取该 canonical Customer 的 resolved 点击；拥有完整 Radar click 范围的专用 Client 可跨客户读取 resolved、pending 和 conflict 点击。一个访问会话的多个技术阶段按第一次成功打开归并为一次 click；pending/conflict 必须有可审计身份链路，匿名和 failed、IP、UA、OAuth 中间加载一律排除。点击响应：event id、radar id/code、click time、`source_system/source_record_id`、`customer_id/identity_status`、授权后的身份投影。  
链接请求不带 `customer_id` 或身份选择器。链接响应：稳定 link id、code、title、enabled/deleted 状态和 cursor；非删除禁用链接保留供历史查询，不返回凭据或配置。

### 6.6 共用错误

`400` 参数/游标错误，`401` token 无效，`403` scope/capability/field/scope 不足，`404` canonical 或记录不存在，`409` identity/order conflict，`429` 限流，`503` 依赖未就绪。`409` 不得被转为空列表。

## 7. Owner 边界和具体文件

| Owner | 现有位置 | 本期允许 | 禁止 |
|---|---|---|---|
| Open Platform | `internal/openplatform/{port,http}`、`cmd/aicrm/open_platform_v1.go`、`open_platform_adapters.go` | operation/catalog、授权、DTO、游标、统一错误、Port 组合 | 直接查其他领域表；恢复旧路由 |
| Identity/Customer | `internal/identity/port`、`internal/customer/port` | 稳定身份解析和显式授权投影；详情字段 profile | 各业务 Owner 自建匹配；隐式建客/合并；把 raw identity 写入日志 |
| Order | `internal/order/{port,app,store}` | 列表过滤、支付时间/退款安全投影、详情 Port | 直接查 payment/radar 表；订单写入 |
| Payment | `internal/payment/port` | 提供 machine-safe payment/refund projection | 暴露回调签名、密钥、原始 effect/provider 私密字段 |
| Survey | `internal/survey/port` | 外部提交投影、历史身份关联、游标适配 | 查询时重评分；直接读别域表 |
| Message archive | `internal/messagearchive/port` | 归档消息安全投影和去重 | Provider 实时读取、发送、旧硬编码员工 |
| Radar | `internal/radar/port` | 新建 click read Port；复用已有 link reader | 复制旧 SQL；暴露 tracking/IP/UA 或配置凭据 |
| Access | `internal/access/{domain,app,http}` | 在 Access0152 冻结后补 scope mode/field profile 合同 | 本期订单 PR 修改员工登录、治理、readiness、0152 migration |

## 8. Terra 首批实施边界

Terra `remaining_export_fixes` 先完成订单列表/详情，最小范围如下：

1. 增加 `order.list`、`order.get` Catalog、REST/MCP 共用 executor 和 `order.read` allowlist。
2. 在 Order Owner Port 之上组合 Payment 只读数据，补齐 paid/refund 筛选、可信 `paid_at`、部分/全额退款和安全详情投影。
3. 每条订单输出来源系统/来源记录 ID、canonical `customer_id`、`identity_status`；支持以 customer_id、已授权手机号和带 scope UnionID 做精确查询，所有解析经 Identity/Customer Port。
4. 保留未绑定订单并明确状态；unknown、pending、conflict 不猜测、不建客、不自动合并；身份歧义返回 409。
5. 实现签名 opaque cursor，绑定 filters、effective grant、field profile、auth version；覆盖篡改、撤销、筛选变化和失败重试。
6. 测试至少覆盖多 provider、未支付/已支付/部分退款/全额退款、无客户订单、手机号往返、UnionID+scope 往返、来源 ID 回溯、conflict 409 和敏感字段未授权。

订单实现不要修改 Access0152 分支中的员工权限、migration、readiness、安装脚本；若 `internal/access/app/machine.go` 或 `domain/machine.go` 发生冲突，先保留最小 capability 增量，scope mode 单独排期。`cmd/aicrm/composition.go` 只做必要 wiring，等 Access 分支冻结后处理冲突。

后续按 Owner 分批：Survey 外部提交、Archive Chat、Radar click/link、Customer detail profile。每批必须复用上述身份链路和覆盖率报告，不接受“各接口自行按手机号/UnionID 查表”。

## 9. 必须拍板的业务问题

以下问题在冻结 OpenAPI 前必须有明确选项和验收样本：

1. 新 Client 的 `explicit_all`、`selected`、`unconfigured` 持久字段和历史 Client 兼容策略。
2. 客户详情 operation/capability 的最终命名，以及手机号、UnionID、OpenID、备注、Owner、跟进员工的字段组边界。
3. UnionID scope 的枚举、跨渠道匹配规则和缺 scope 的错误码。
4. 订单 `paid_at` 的可信来源、退款处理中是否计入 `is_refunded`、退款记录保留范围和订单源字段缺失处理。
5. 问卷 native 与历史记录的来源标识、答案快照解密失败的 unavailable 表达和覆盖率阈值。
6. Chat 私聊员工、群参与者、媒体内容的授权投影和归档去重规则。
7. Radar click 的逻辑事件判定、identity conflict 返回面和历史覆盖率。
8. 游标水位、授权绑定、auth version 变化后的错误码，以及真实工作台提供后端到端验收责任。

## 10. 验收与发布门槛

验收必须包含：

- token scope/capability 组合、拒绝 write、撤销立即生效；
- canonical customer_id、手机号、UnionID+scope 三种方向的查询往返；
- 唯一、未知、pending、conflict 身份及 409 行为；
- 七项能力均保留来源系统和原始记录 ID；
- 真实历史覆盖率和缺失清单，不以空结果代替缺失数据；
- 订单支付/退款、问卷快照、Chat 私聊/群聊/重复/媒体、Radar 事件去重/禁用链接；
- 游标篡改、过滤变化、授权变化、失败重试；
- 审计、脱敏、Secret/PII 不进日志；
- CRM/API 真实读路径和外部工作台同步回读。工作台仓库或服务器未提供前，只能标记 CRM 侧完成，不能宣称端到端完成。

## 11. 证据与参考

- PR#173 已通过 Connector 核验为 merged，merge commit 为 `f1ea84caa0070bbd4cc06d6d0628999d31567dc7`；本 PR 只作为 v3 Open Platform 基线证据。
- 旧仓只读 donor 的 HEAD 为 `dd8d60dd8ddb983aca2ec88cc9e65a9f7563f79f`；仅用于行为、字段和历史数据缺口对照，不复制目录或运行时依赖。
- 订单脱敏参考文件仅用于字段行为核对；禁止打开或传播含真实 Secret 的原始文件。
