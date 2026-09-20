---
name: aicrm-v3-development-frontdoor
description: "AI-CRM-v3 的开发前置门禁与并行交付流程。用于新功能、Bug 修复、调试、合并和上线前规划；要求先完成市场/GitHub 调研、复用评估和冻结 PRD，并持续推进到 GitHub 合并、SSH 部署和上线验收。"
---

# AI-CRM-v3 开发前置门禁

先阅读 `AGENTS.md`、`skills/aicrm-v3-development/SKILL.md`；涉及前端时再阅读 `skills/aicrm-v3-frontend-consistency/SKILL.md`。详细字段见 [开发前置流程与上线验收标准](../../docs/plans/开发前置流程与上线验收标准.md) 和 [PRD 前置模板](../../docs/prd/开发前置PRD模板.md)。

## 开始前分类

记录：

```text
OneID：不涉及 | 读取 canonical customer | 解析身份 | 建立客户 | 关联/合并身份
Persistence：stateless | 本地事务 | 内部持久任务 | Provider 读取 | Provider 写入/外部效果
```

不涉及的轴必须写明原因，不得制造虚假依赖。

## 新功能三步门禁

编码前必须完成：

1. 至少 2 个成熟产品/公开方案和至少 1 个高质量 GitHub 参考；找不到时记录搜索范围和结论。
2. 评估仓库已有领域、共享组件、标准组件、OneID、持久化和 External Effects，明确采用、扩展、舍弃。
3. 形成完整 PRD，包含业务逻辑、成功标准、接口/数据/权限边界、架构分类、测试、上线、监控、回滚和并行依赖。

PRD 经用户确认后冻结；重大范围、合同或风险变化必须重新确认。未完成三步不得正式编码。

## 一次闭环交付

当所有假设和风险边界确认、用户明确开始开发后，持续推进到完整上线验收，不把常规实现选择逐步退回用户。只有新的业务决策、红线风险、凭据/权限缺失或部署证据不一致才暂停并报告。

完整终点：实现 → 本地完整验证 → 预发布机验收 → GitHub 轻量一致性门禁与合并 → 生产 SSH 部署 → 部署后认证读回 → 观察窗口验收。

常规发布先在 `49.232.57.128` 预发布机完成完整部署和合成业务验收，再合并 PR；PR 只验证预发布 receipt 与当前 tree 一致、治理和冲突状态。合并后从本地通过 SSH 登录 `124.220.53.183` 完成生产部署。生产部署私钥固定使用 `/Users/qianlan/Downloads/zhengshi.pem`，必须保持 `0600`，不得复制到仓库、PR、日志或命令输出。预发布使用同一账号和密钥时也必须单独核验 Host Key。

本地构建发布包必须使用 Linux CI 等价的运行时目标：固定设置 `GOOS=linux`、`GOARCH=amd64`，并为 Linux amd64 cgo runner 提供显式交叉编译器（例如 `CC="zig cc -target x86_64-linux-gnu"`）。发布包统一由仓库 Python archiver 创建，拒绝 `._*` AppleDouble、symlink、未注册文件和非 Linux ELF；不再直接使用 macOS BSD tar。`release-files.sha256` 必须在本地、预发布安装器和 success observer 中通过。

当前已验证 SSH 账号为 `ubuntu`，通过 `deploy/run-release-as-root.sh` 持有 root fd 9 后执行 installer；禁止从普通 sudo 调用中传递失效的锁描述符。使用 `-i /Users/qianlan/Downloads/zhengshi.pem -o BatchMode=yes -o IdentitiesOnly=yes -o StrictHostKeyChecking=yes`，显式选择已核验的 known_hosts；禁止尝试其他账号或绕过校验。上传前后独立比对 SHA-256，逐个检查 `bin/` 下文件为 Linux x86-64 ELF，纯 Go 默认 `CGO_ENABLED=0`，SDK runner 由现有脚本单独开启 cgo。构建必须来自准确候选 tree 的干净独立 checkout。

同一发布仅允许一个任务负责构建、上传、安装和观察，其他任务排队；不得共用可被另一任务改写的发布目录或归档路径。安装失败先保留证据并检查实际 active SHA；没有新证据不重复安装，不直接修改运营配置或跳过 bootstrap。失败候选需隔离时，先在发布锁下确认未运行、无成功收据和其他引用，不能删除正在使用的 release。

## 并行开发与发布队列

每个板块、Bug 或调试任务登记负责人、分支/worktree、状态、修改范围、共享入口、依赖项和预计上线窗口。开发与合并前盘点活跃分支、PR、Debug 任务、重叠文件/模块、迁移/API/组件合同、未部署提交、排队版本、部署和观察窗口。

- 每个板块使用独立 `codex/` 分支或 worktree。
- Composition、公共组件、迁移、CI、Provider 和部署脚本串行合并。
- 普通领域可并行，但合并前同步最新 `main` 并重跑受影响验证。
- Debug 分支不能作为稳定依赖；必须先形成可验证提交或明确临时接口。
- 一次只执行一个生产部署；部署后独立读回版本、迁移、健康、管理员页面和真实业务结果。

存在部署未完成、未知结果、回滚未完成、共享合同顺序依赖或过期 HEAD 时暂停合并并重新排队。

## Bug 修复

保存真实失败证据，判断根因和影响面，先补回归测试或旅程，再做最小根因修复，执行原失败阶段和受影响全链路，并记录防复发措施。除非用户明确标注线上热修，不得走简化流程。

## 前端门禁

新增或修改前端必须使用 [@Product Design](plugin://product-design@openai-curated-remote) 和 [$artifact-template-crm](/Users/qianlan/.codex/skills/artifact-template-crm/SKILL.md)，优先复用管理端壳、标准选择器和共享组件。禁止单页私造标签、话术、群组、客服、商品等标准组件；仅文案、样式 token 或既有页面缺陷修复可走 audit/consistency 复核。

## 测试与合并

按影响范围先在本地执行适用的静态、编译/单元、PostgreSQL/迁移/事务、集成/权限、前端构建与真实浏览器、OneID、External Effects、发布包和回滚检查；预发布机再执行完整部署、合成数据业务旅程和读回。GitHub PR 默认只执行一致性、治理、冲突、证据和 tree 检查，不重复本地长测试；修改 Composition、迁移、Provider、共享组件、CI 或部署脚本时，可由维护者显式升级完整云端 CI。

PR 保留准确 HEAD/tree、并行与发布快照、测试摘要、证据目录和未验证项。编译通过、排队成功、Mock 或 HTTP 202 都不能单独称为完成。

## PR 证据留存

- PR 首轮 run/attempt/head/结果一旦产生不得覆盖；后续修复只追加事件。
- 当前 head 的最终结果只能由当前 head 的最新完整 required check 决定，旧 head 的绿灯不能替代。
- 固定分类为 `assertion_or_verification_failure`、`environment_setup_failure`、`cancelled`、`pending_or_incomplete`、`unknown_failure`；本地环境阻塞不能写成代码失败，替代环境通过不能冒称原环境通过。
- 每次修复必须记录修复提交、重跑阶段和最终结果；PR 正文只放轻量摘要与 artifact 链接，详细日志留在 artifact。
- 合并前必须有首轮和最终证据，未完成 lane、取消、skip、unknown 必须显式列出；合并后继续记录 merge SHA、部署 SHA、认证读回和观察窗口。
- 轻量 quality summary、首轮失败 manifest 和最终 check manifest 保留 90 天；详细 lane 日志、截图和大型测试包沿用 14/30 天策略。
