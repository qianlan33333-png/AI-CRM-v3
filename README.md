# AI-CRM v3

AI-CRM v3 是单企业私有化 CRM 的 Go 主运行仓库。本期已完成可上线的基础能力：PostgreSQL 平台底座、OneID 身份内核、员工认证与权限、企微 OAuth/回调/JSSDK/侧边栏身份，以及从旧前端白名单复用的管理后台和侧边栏壳。

固定供体基线：

- `AI-CRM-production@4af15e64fb7ebb311b52b17eaf5fc5ea5e8154c8`：生产行为与 OneID 参考；
- `AI-CRM@69c5282fb38058f2cc9872b6feb3f0f54bfad64b`：管理后台和企微侧边栏视觉壳。

供体仓不是运行时依赖，也未复制其 Git 历史。

## 本期能力

- 渠道中立 `customers.id`、外部身份唯一约束、冲突、关联意图、合并候选、管理员确认归并和可审计撤销；
- 本地 Argon2id 密码、数据库 Session、CSRF、登录限流、固定三角色和 `session_version` 即时失效；
- 企微员工登录、侧边栏 OAuth、JSSDK 签名、客户上下文令牌、外部联系人加密回调和幂等 Inbox worker；
- 后台完整 CRM 菜单、登录/首页/员工权限/OneID 查询页和侧边栏壳；尚未开发的业务统一显示“功能待接入”，不会调用旧 API；
- 支付宝仅实现通用身份 Provider 契约和 Fake Adapter，不包含支付宝网络调用、订单或支付；
- `main` 必过 `make check`，成功后通过固定 SSH 主机密钥自动发布版本化 release。

公开 HTTP 契约见 [OpenAPI](api/openapi.yaml)，数据迁移见 [migrations](migrations)，部署约束见 [部署说明](deploy/README.md)。

## 本地运行

需要 Go 1.26 和 PostgreSQL 16。先创建数据库并执行迁移：

```bash
export AICRM_DATABASE_URL='postgres://aicrm:password@127.0.0.1:5432/aicrm?sslmode=disable'
go run ./cmd/migrate-platform
```

首次启动可用环境变量幂等创建超级管理员：

```bash
export AICRM_BOOTSTRAP_USERNAME='admin'
export AICRM_BOOTSTRAP_PASSWORD='replace-with-a-strong-password'
export AICRM_BOOTSTRAP_DISPLAY_NAME='系统管理员'
make run
```

默认只监听 `127.0.0.1:8080`。公网流量由 Caddy 在 80/443 终止 HTTPS 后反向代理：

```bash
curl http://127.0.0.1:8080/healthz
curl http://127.0.0.1:8080/readyz
```

`AICRM_WECOM_ENABLED=false` 是安全默认值。启用企微必须一次性提供完整的企业、应用、回调和上下文签名配置；支付宝没有启用开关。

企微标签目录读取另有最窄的独立开关：`AICRM_WECOM_TAG_CATALOG_PROVIDER_ENABLED=false`。`id-dev` 保持关闭；只有同时明确配置 `catalog-read-authorized` 权限时才允许启用，只读取企业标签目录，不包含客户打标或去标。

## 验证

```bash
make check
go test -race ./cmd/aicrm ./internal/access/... ./internal/identity/... ./internal/wecom/...
govulncheck ./...
```
