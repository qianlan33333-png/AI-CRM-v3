# AI-CRM v3

AI-CRM v3 是下一代 CRM 主运行仓库。它从干净 Git 历史开始，以白名单方式吸收两个供体仓的有效资产：

- `AI-CRM-production`：提供 OneID、企微授权和支付关联的生产行为真值；
- `AI-CRM-v2`：提供 Go 领域实现、基础设施、Provider Adapter 和测试素材。

旧仓不是 v3 的运行时依赖，也不会以 Git 子模块、远程服务或整目录复制方式接入。

## 当前状态

这是仓库初始化基线，已包含：

- 迁移范围 PRD；
- 模块化开发与交付方案；
- 最小 Go HTTP 服务；
- `/healthz` 与 `/readyz`；
- Customer、Identity、Access、WeCom、Order、Payment 的目录边界；
- scoped Identity Ref 的最小实现与测试；
- 模块注册和供体基线清单。

当前没有任何业务模块被声明为生产就绪，也没有真实企微、支付、迁移或生产部署能力。

## 核心决策

1. Go 模块化单体；
2. PostgreSQL 是业务事实和持久异步任务的唯一有状态基础；
3. `customers.id` 是渠道中立 OneID；
4. 外部身份只进入 Identity 域；
5. 一个领域只有一个写入者；
6. 按用户 Journey 和路由逐项切流；
7. 供体代码必须先归类为 Behavior、Port、Adapter 或 Discard。

## 文档入口

- [搬迁范围与新仓库基线 PRD](docs/01-PRD-迁移范围与新仓库基线.md)
- [模块化开发与交付方案](docs/02-模块化开发与交付方案.md)
- [供体基线](docs/source-baselines.yaml)
- [模块注册表](modules/registry.yaml)

## 本地启动

目标工具链记录在 `.tool-versions`。安装对应 Go 版本后：

```bash
make check
make run
```

默认监听 `127.0.0.1:8080`：

```bash
curl http://127.0.0.1:8080/healthz
curl http://127.0.0.1:8080/readyz
```

## 发布到 GitHub

当前目录已经初始化为本地 Git 仓库。具备 GitHub CLI 登录环境时运行：

```bash
scripts/publish-github.sh qianlan33333-png/AI-CRM-v3 private
```

公开仓库需显式把最后一个参数改为 `public`。
