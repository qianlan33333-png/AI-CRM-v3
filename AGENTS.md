# AGENTS.md

本文件适用于整个 `AI-CRM-v3` 仓库。

## 1. 仓库地位

- v3 是唯一新能力主线。
- `AI-CRM-production` 与 `AI-CRM-v2` 只能作为只读行为、测试和叶子协议供体，禁止成为 Go module、submodule、远程运行依赖或正常数据源。
- 新功能不得在 v3 和旧仓重复实现。
- 用户最新明确指令 > `docs/01-PRD-迁移范围与新仓库基线.md` > `docs/02-模块化开发与交付方案.md` > 本文件。

## 2. 固定架构

- Go 模块化单体、PostgreSQL 16、单企业、单数据库。
- `cmd/aicrm` 是唯一 Composition Root；只负责加载配置、创建平台设施、注册模块和启动角色。
- 跨领域只允许 import `internal/<domain>/port`、稳定值对象或使用版本化领域事件。
- 禁止跨领域 import `app`、`store`、`http`、`worker`、`provider` 或生成物。
- `internal/platform` 不得 import 业务领域。
- 每张业务表只有一个 Owner；Store 只访问本领域拥有的表。
- 不自行引入微服务、多租户、Redis、Kafka、进程内 cron/ticker 或 Kubernetes 前置。

## 3. OneID

- `customers.id` 是唯一渠道中立业务主键。
- 外部身份归 `identity`，必须包含 kind、scope、value、assurance、source。
- OpenID 没有 App scope 时不得匹配；UnionID 没有开放平台 scope 时不得跨渠道关联。
- `verified` 只能由完成 Provider 验证的内部 Adapter 构造；HTTP 请求体不能自报升级。
- 无唯一可信证据时保持 pending/conflict，不猜测客户。
- `Resolve` 只解析，不隐式建客；建客必须走显式 `ProvisionCustomerFromVerifiedIdentity`。
- Identity 首版不做破坏性自动合并；跨 Customer 根连接形成可审计 merge candidate。

## 4. 数据与事务

- 业务状态、幂等收据、审计和 Outbox 必须在同一 PostgreSQL 事务提交或回滚。
- Provider 网络调用不得持有数据库事务。
- 并发更新使用显式锁、CAS 或版本号。
- v3 迁移从独立序列开始，不复制旧仓 migration 历史。
- 迁移工具放在 `cmd/migrate-*`，运行时包不得 import 迁移器。

## 5. 外部效果

- Provider 默认 disabled。
- 外部调用必须区分 accepted、queued、attempted、executed、outcome_unknown、reconciled。
- `outcome_unknown` 禁止盲目换幂等键重试。
- 支付、退款、企微写入和群发必须具备签名/验签、幂等、回调重放、对账和审计。
- Token、Cookie、OAuth code、Secret、私钥、openid、external_userid、手机号不得进入结构化日志。
- 只有 `outbound` 可以拥有企微业务写调用；其他模块只提交意图。

## 6. 开发单位

- 一个 PR 交付一个用户可观察能力或一个明确缺陷。
- 不以目录存在、接口骨架、HTTP 200、Mock 或排队成功作为完成。
- 旧能力迁入前先冻结 Behavior Contract 和 Characterization/Journey 测试；禁止整目录复制后再清理。
- 临时兼容 Adapter 必须登记 Owner、替代路径和删除条件。

## 7. 红线

遇到以下情况立即停止该实现并报告：双主写、身份错误归属、支付/退款/Provider 效果可能重复、鉴权绕过、Secret/PII 泄漏、跨领域表写入、不可逆数据损坏、迁移静默丢数据。
