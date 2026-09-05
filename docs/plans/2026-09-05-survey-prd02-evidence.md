# Survey PRD-02 实施与验收证据

本轮分类：问卷 OAuth/外部身份通过既有 Identity Port 解析；提交、Survey 回执、审计、Outbox 与 External Effects 接纳在同一 PostgreSQL Unit of Work 中提交。Provider 网络调用在事务外，默认关闭。

| 能力 | 实现与覆盖 |
| --- | --- |
| 提交完成外推 | `SubmissionService` 在提交事务中冻结配置、结果、答案版本及加密发送身份，绑定 `KindSurveyCompletion` 效果；重复提交复用既有绑定。 |
| 外推协议与安全 | Outbound 仅在启用的配置目标执行，使用注册 Webhook 的 client-id、Unix 秒 timestamp、event-id、HMAC 签名；禁止重定向，端点和查询参数不进入错误文本。 |
| 身份和数据边界 | 缺少可信身份保留未发送状态；身份读取错误使接纳事务回滚。外部身份、手机和答案均不进入 EER、日志或明文 JSON。 |
| 配置与旧页面入口 | Host Adapter 在问卷操作页提供 type/day/frequency/expires/remark 与键值 custom params 的编辑、重开回填和保存；URL/Secret 仍只属于部署配置引用。 |
| 并发保存 | metadata 写入的 `configuration_version` 是 CAS 条件。页面在写前读取版本；后端和 PostgreSQL 更新均验证版本。409 时界面显示重新打开提示，不显示已保存。既有运营页面的仅开关/引用 PUT 保持兼容并保留 metadata，仍由服务端 CAS 保护。 |

本地已通过：

```
GOCACHE=/private/tmp/aicrm-survey-gocache GOFLAGS=-p=1 go test ./internal/survey/app ./internal/survey/http ./internal/survey/store
node internal/webshell/static/admin_console/survey_qr_bridge.test.mjs
```

第二个命令覆盖零回执页面可编辑、CSRF header、参数回填保存以及 409 冲突提示。HTTP 单元测试覆盖 CSRF 拒绝、过期配置版本，以及既有 `saveQuestionnaireOpsDto` 的 completion→external-push 顺序保存后 metadata 不丢失；前端契约测试确认该旧 DTO 仍只发送开关和配置引用。PostgreSQL 集成测试覆盖 A 读取配置、B 改开关和引用、A 带旧版本保存时的冲突，以及审计和 Outbox 无残留。

本机未配置隔离 `DATABASE_URL`；本地运行该 PostgreSQL 集成测试会明确跳过。此前尝试启动本机隔离 PostgreSQL 被沙箱共享内存限制阻断，未触及任何已有数据库。PR CI 的 PostgreSQL 16 service 是该集成测试的待验证环境；本文不将其标记为已通过。
