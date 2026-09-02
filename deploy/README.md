# Deploy

部署目标为单仓模块化单体，可按 `api`、`worker` 或 `all` 角色运行。任何真实环境配置、Secret、域名、机器地址和 Provider 开关都不进入初始化提交。

## 最小运行形态

当前 Bootstrap 只需一个 Linux 可执行文件和 systemd，不执行真实企微、支付或数据迁移。

```bash
AICRM_RELEASE_SHA="$(git rev-parse HEAD)" scripts/build-linux.sh amd64
```

部署机的固定路径：

- 可执行文件：`/opt/aicrm/bin/aicrm`
- 环境配置：`/etc/aicrm/aicrm.env`
- systemd unit：`/etc/systemd/system/aicrm.service`

复制 `deploy/aicrm.env.example` 到服务器后，将 `AICRM_RELEASE_SHA` 改成实际提交 SHA。Secret 只能通过服务器配置管理，不允许写入 Git。

上线后验收：

```bash
curl --fail --silent --show-error http://127.0.0.1:8080/healthz
curl --fail --silent --show-error http://127.0.0.1:8080/readyz
```

当前没有 PostgreSQL 或 Provider 依赖，因此 readiness 只表示 Bootstrap 已就绪。后续 Platform Kernel PR 必须将它替换为真实依赖检查。
