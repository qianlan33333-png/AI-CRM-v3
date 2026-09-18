# CRM 核心人群 API 标准接线

按用户提供分工文档 A1/A2 修复：五个已有业务接口进入最外层路由与既有 Operation Catalog，细分读写能力沿用 OAuth 客户端管理；不自动扩大已有生产客户端权限。

OneID: reads canonical customer via existing Segment and trusted owner ports. Persistence: push record reuses Segment transaction, receipt and audit; reads use standard operation audit. No new identity, queue, OAuth issuer or Provider writes.

GitHub reference: [existing V1 Operation Catalog](https://github.com/qianlan33333-png/AI-CRM-v3/blob/main/internal/openplatform/port/operations.go) and `cmd/aicrm/open_platform_v1.go`; reuse verified catalog/invocation implementation without a second permission system.

新增能力：audience.product.read、audience.member.read、audience.member.operations.read、audience.member.history.read、audience.push.write。REST 与 MCP 使用同一个 executor；写记录稳定 push_id、幂等键和状态版本保留。

数据范围：package_id 先校验；客户级复用可信 owner/customer 校验；成员分页逐项过滤，原游标继续可分页，不返回越权成员数据。产品目录只依包范围过滤，客户范围不能泄漏客户数据。

验收：真实 OAuth token 与最外层 Host 路由、授权目录、未授权/只读/越权拒绝、重复上报仅一条、状态更新、数据库回读。文档同步 OpenAPI 和标准控制台目录。XC R1/R2/R3 的实际地址/凭据/回流证据单独跟踪，不误报 S5。

本地证据：`/tmp/core-api-tests-final.log`、`/tmp/core-api-oauth.log`（真实组合服务＋OAuth＋PostgreSQL）、`/tmp/core-api-domains.log`、`/tmp/core-api-frontend.log`、`/tmp/core-api-openapi.log`。fast/compile 见 `/tmp/core-api-fast-final.log`、`/tmp/core-api-compile-final.log`。这些是专项证据，不替代完整 CI 或生产验收。

回归稳定性修复：CI 的素材刷新旅程复现导航期间 CDP 错误及旧 DOM 按钮误判。提交分组变更后等待新 loader 和文档就绪；只读轮询兼容导航瞬时错误，写动作不重试，原断言全部保留。本地修复前 8 次重复中复现两类错误，修复后完整旅程连续 8 次通过（`/tmp/core-api-media-repro-repeat.log`、`/tmp/core-api-media-fixed.log`）。仅测试同步，不涉及 OneID 或生产持久化。
