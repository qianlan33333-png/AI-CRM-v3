# PRD：分区式客户档案与跨领域能力接线

状态：本期代码已实现；真实 PostgreSQL 集成验收待 CI 或可用测试数据库执行

冻结供体：`AI-CRM@dd8d60dd8ddb983aca2ec88cc9e65a9f7563f79f`

目标仓库：AI-CRM-v3（Go 模块化单体）

## 1. 开发门禁结论

- OneID：涉及。所有入口只接受数字 `customer_id`；先解析 canonical Customer，再读取档案。详情读取没有 Provision、Merge、assurance 升级或手机号解析权限。
- 持久化与任务：涉及。企微全量同步沿用 River 持久任务，在 Provider 调用结束后进入 PostgreSQL 事务；观察、同步收据、审计、Outbox、目录和时间线在现有事务边界内提交。
- 外部效果：不涉及 Provider 写。打开详情页只读 v3 PostgreSQL，企微 Provider 调用次数必须为零；聊天与问卷分区也不得通过旧 HTTP 或旧库兜底。

## 2. 供体行为与差异

供体 `/admin/customers` 以 UnionID 或 external_userid 打开详情，随后并行读取基础档案、标签、问卷和消息；`/admin/user-ops/ui` 以 external_userid 并行读取用户详情与最近 20 条时间线。供体展示手机号、负责人、外部标识、标签、问卷题目/答案和聊天正文。

v3 只迁入“二级详情页、并行分区、独立状态、独立重试”的可观察行为。以下行为禁止迁入：

- UnionID、external_userid、手机号多级兜底找客；
- raw external_userid、UnionID、corp scope、完整手机号常驻页面；
- 页面打开时实时访问企微；
- 把本地标签投影描述成实时 Provider 结果；
- 返回聊天正文、发送人/接收人 Provider 标识或原始载荷。

## 3. 用户目标与范围

从 `/admin/customers` 的“查看详情”进入 `/admin/customers/{customer_id}`。基础档案先展示，其余分区并行加载；任一分区失败不得遮挡其他分区。

本期交付：

1. 基础档案、OneID 安全摘要、手机号脱敏和 30 秒授权查询；
2. 企微跟进成员观察、企微标签最近同步快照；
3. 按 canonical Customer 查询脱敏问卷历史；
4. Customer 安全时间线投影；
5. 聊天活动摘要契约与明确 `not_ready` binding。

不做：客户、负责人、阶段、标签写入；订单支付与权益；聊天正文；手机号/昵称推断身份；运行时访问旧仓或旧生产库。

## 4. 页面信息架构

视觉实现冻结为供体 `AI-CRM@dd8d60dd` 的客户列表与客户档案页面。v3 直接复用供体的 `admin-card`、`admin-filter-bar`、`admin-data-table`、`admin-module-banner`、`admin-profile-grid`、`admin-split-grid`、`admin-message-list` 等结构和样式类，只替换 Go Template 数据入口与安全 API 接线。不得以“功能等价”为由改成单列信息块、通用简易表格或另一套视觉设计。

### 4.1 一级客户列表

按供体双卡片结构展示“客户查找”和“客户列表”：筛选条件在第一张卡片，结果表格与分页在第二张卡片。企微同步状态、刷新/重拉作为第二张卡片内的 v3 增量能力。入口 URL 只能包含数字 Customer ID。

### 4.2 二级客户档案

按下列顺序展示：

- 顶部沿用供体 `admin-module-banner` 和五列 `admin-profile-grid` 摘要；
- 主体沿用供体左右双栏，左侧放跟进成员、企微标签、问卷，右侧放聊天记录与客户时间线；
- 供体已有页面级标题与模块 banner 标题同时保留，作为原 UI 结构的一部分。

1. 基础档案：姓名、Customer ID/CID、状态、企业、客户类型、来源、最后同步时间。若请求的是 merged Customer，展示来源 ID 与 canonical ID。
2. 手机号：默认不带 `+86` 的脱敏值；不显示 assurance。Admin/SuperAdmin 点击“查询”后展示 30 秒；Viewer 禁止；响应 `no-store` 并审计。
3. 跟进成员：展示已映射显示名、关系状态和最近观察时间；未映射成员只汇总数量，不回显 raw userid。多成员全部展示，不命名唯一负责人。
4. 企微标签：标题为“企微标签（最近同步）”；展示名称和观察时间。目录未映射时显示“标签名称待同步”，绝不显示 Provider tag ID。
5. 问卷记录：标题、提交时间、分数/安全测评摘要、题目和选项文本；自由文本与手机号答案只使用 Survey 提供的脱敏值。
6. 客户时间线：事件类型、标题、来源领域、时间；无正文、Provider 标识或载荷。
7. 聊天活动摘要：冻结私聊/群聊、消息类型和时间契约；来源模块未完成时显示“能力待接入”。

分区状态为 `ready | degraded | not_ready`。`not_ready` 与运行故障必须使用不同文案；空数据是 `ready` 下的 empty，不得伪装成待接入。

## 5. HTTP 契约

- `GET /api/admin/customers/{customer_id}`：基础档案、安全身份/手机号摘要和 `sections` readiness。
- `GET /api/admin/customers/{customer_id}/owners`
- `GET /api/admin/customers/{customer_id}/tags`
- `GET /api/admin/customers/{customer_id}/survey-answers?cursor=&limit=`
- `GET /api/admin/customers/{customer_id}/timeline?cursor=&limit=&event_type=`
- `GET /api/admin/customers/{customer_id}/chat-activity?cursor=&limit=&chat_type=`

分页默认 20、最大 100。cursor 使用服务端 HMAC 签名，绑定 section、canonical customer、筛选和固定 watermark。成功分页响应包含 `customer_id, items, next_cursor, source_status, as_of, truncated`。

- 未接入：`503 capability_not_ready`
- 已接入但故障：`503 section_unavailable`
- Customer 不存在或不可见：`404`
- 所有详情 GET：`Cache-Control: private, no-store`

## 6. Port 与领域 Owner

Customer 发布 `CustomerOwnerReader`、`CustomerTagReader`、`CustomerSurveyReader`、`CustomerTimelineReader`、`CustomerChatActivityReader` 五个强类型消费 Port。Adapter 只在 `cmd/aicrm` 组合；Customer 不 import 来源领域的 app/store/http/provider。

- Customer：`customer_directory_projection`、`customer_timeline_projection`。
- Identity：Customer 根、身份和 canonical 链路；详情只有读取权限。
- WeCom：跟进成员与客户标签观察；完整成功轮次才 stale，部分失败保留上一轮成功快照。
- Tag：Provider tag ID 到最近本地目录名称的只读映射。
- Survey：按 Customer ID 和固定 watermark 的脱敏历史读取 Port。
- Chat：本期 disabled binding，不建立伪数据或旧接口兜底。

## 7. 数据与隐私规则

- WeCom 表可在领域内部保存 employee userid 与 provider tag ID 作为作用域明确的观察键，但 Customer API、日志和审计不得回显。
- Customer 时间线只保存安全标题、事件类型、来源领域、来源事件 ID 和时间。
- 时间线以 `(source_domain, source_event_id)` 幂等，迟到事件按 `(occurred_at DESC, id DESC)` 稳定排序。
- 标签目录不可用时保留关系事实并降级展示；不得把 ID 当名称。
- Survey 只按 canonical customer_id 查询；不按手机号或外部身份回查。

## 8. 状态、故障与回滚

- 企微批量页提交时更新本页观察；同一客户可同时保留多个成员关系。
- 只有完整全量同步进入 reconciling 后，才将本轮未观察到的成员/标签标记 stale。
- 部分失败或 Provider disabled 不清空历史成功观察。
- 回滚应用时关闭客户同步开关并回退二进制；0022 为前向兼容迁移，不删除观察、时间线或审计事实。

## 9. 验收

- 列表详情链接只含数字 customer_id；merged ID 解析到 canonical root。
- 打开详情页企微网络调用为零。
- 单分区 503 时其他分区保持可用并可独立重试。
- 重复全量同步不重复成员/标签；多成员不被唯一客户收据去重丢失；部分失败不 stale。
- 标签接口不含 provider tag ID；成员接口不含 raw userid。
- Survey 自由文本/手机号只返回脱敏值，跨 Customer 隔离。
- Timeline 重放不重复，cursor 篡改或筛选漂移返回 400。
- Chat 不返回正文、sender、receiver、external_userid 或载荷；未接入明确 503。
- Viewer 无法查询明文手机号；Admin/SuperAdmin 查询被审计且 30 秒后从 DOM 清除。
- 必跑 `make check`、相关 Go 测试与 race、OpenAPI 校验、PII 字符串扫描。

## 10. Frontend Skill Checklist 与实现核对

- 已完整读取供体 `frontend-development-skill.md`；参考供体客户列表、客户档案、用户运营详情及 v3 当前客户页面。
- 复用 v3 admin card、state、table、button、Go Template、原生请求封装和 OpenAPI 契约；未引入 React 或新构建链。
- 新增通用详情分区状态渲染器，统一处理 ready、degraded、not_ready、empty、error、retry 和加载更多；新增原因是现有页面没有覆盖跨域分区状态的共享实现。
- 一级页面只保留列表、筛选、分页、同步和导航；二级页面承载全部档案分区，并完整保留供体的页面标题、模块 banner、摘要栅格和左右双栏。
- 未新增第二套 Customer ID、身份解析、标签目录或问卷存储；运行时不依赖供体仓库、旧数据库或远程兼容 API。
- 本地已通过 `make check`、相关模块 `go test -race`、OpenAPI 校验、JavaScript 语法检查、前端 434 项测试和 PII 字符串扫描；`govulncheck` 未发现可调用漏洞。
- 当前执行沙箱禁止 PostgreSQL 所需共享内存，0022 migration、企微同步和手机号导入的真实数据库 Journey 必须在 CI 或可用 PostgreSQL 16 测试库补跑，未通过前不得部署或宣称生产完成。
