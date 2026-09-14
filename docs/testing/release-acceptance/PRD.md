# CRM 全量上线测试：隔离执行 PRD

## 目标与边界

对候选版本 `dcfc02290daca3f4bb9509120698e9e976b5369b` 建立可重复、可审计的发布验收。它验证候选源码、受控 PostgreSQL 数据、管理端与受控 Provider 合同；不暂停线上业务、不连接生产数据库或队列、不使用生产凭据，也不向真实客户、群或支付渠道写入。

只有候选 SHA、测试 harness SHA、首次/最终执行结果和证据目录可一一对应时，结果才可用于上线评审。文档、coverage inventory 或 harness 发生改动后，必须先提交，再从干净工作树重新执行；不得将脏工作树的结果归属给候选版本。

## 业务判断与架构分类

- 用户与成功标准：发布负责人需要知道该版本能否安全进入白名单业务验收；成功指核心业务闭环、权限、回读和恢复路径有同 SHA 的通过证据，且所有必测项已有最终状态。
- 禁止行为：把 HTTP 200、排队成功、Mock、截图或一次重跑替代业务闭环；把未运行或未关闭的 skip 记为通过；将真实 Provider receipt 伪装为本地测试。
- OneID：验收对象涉及客户、渠道与外部身份时，测试应读取和解析既有 `identity/port` 及 `customers.id` 合同。隔离 harness 自身不解析、建客、链接或合并身份；它只提供空的本地测试库。
- Persistence：本地事务和内部持久任务在唯一的 `aicrm_test_*` PostgreSQL 16 数据库验证。业务状态、幂等收据、审计和 Outbox 的原子性由既有 backend/browser 用例验证，不由新脚本复制实现。
- External Effects：本阶段不涉及 Provider 写入。harness 删除继承的 `AICRM_*` 配置并强制所有已知 Provider 开关为 disabled，防止运行时读取真实连接信息。真实白名单验收是单独门禁，须使用指定测试身份并保存 Provider 实收/到账及 CRM 回读。
- 前端：本次不修改页面、组件或资产。浏览器验收仅复用现有 shell/Journey；故没有新增或扩展公共组件。终端基线为管理端、企微侧边栏、H5 和公共页各自已有入口。

## GitHub 参考与采用结论

- [Frappe CRM 的 Playwright 配置](https://github.com/frappe/crm/blob/develop/playwright.config.ts) 使用认证 setup project、单 worker、CI retry、首次重试 trace 和失败视频/截图。采用其“串行可复现、失败留证”的原则；不复用其登录状态或测试框架。
- [Playwright CI 指南](https://playwright.dev/docs/ci) 要求 Linux agent 具备可运行浏览器的环境，并建议 CI 单 worker 以优先稳定可复现。采用为 browser/archive SDK lane 的 Linux amd64 前置条件；本机 macOS 不把缺少 Chrome 误记为业务失败。
- 本仓 [quality_lanes.py](../../../scripts/ci/quality_lanes.py) 是唯一 CI/local lane 命令定义。本 PRD 只为它加隔离调用入口，不复制或改写 lane。

## 执行规则

1. 冻结 `origin/main` SHA，创建独立工作树；记录候选 SHA。
2. 提交 PRD、报告模板和隔离入口，记录 harness SHA；从该 harness 的干净 checkout 指向另一个精确候选 SHA 的干净工作树执行，候选和 harness SHA 分开报告。
3. `scripts/testing/run_release_acceptance.py` 只接收 `localhost`、`127.0.0.1` 或 `::1` 的 `postgres://` URL，数据库名必须匹配 `aicrm_test_[A-Za-z0-9_]+`，查询参数仅可使用 `sslmode=disable|prefer|require`。它保留调用者 `HOME`，从最小允许列表构造子进程环境，并以 task-specific 目录隔离 Go/XDG 配置；显式禁用 PostgreSQL、npm、pip 和 Git 的用户配置读取，清除 PostgreSQL service、host override、password file、options、通用数据库 URL 和所有遗留 donor alias。
4. 按现有 quality lanes 分别运行 preflight、backend、frontend、browser、archive-sdk。backend 可不需要 Chromium；browser 必须单独在 Linux amd64 + Chrome + Noto Sans CJK SC 环境运行。
5. 每次执行生成不可覆盖的 `run_id` 目录，持久化脱敏的 stdout/stderr、开始/结束 UTC 时间、实际无凭据命令、候选和 harness 的 SHA/tree/dirty 前后快照。对每一项 skip 记录原因、适用性、关联用例、候选 SHA 和闭合证据。仅当同一候选 SHA 的明确执行 lane 已通过该用例时，skip 才能关闭。平台不适用专项不会自动计业务失败，但未关闭的必测 skip 阻断上线。
6. 缺陷修复后先重现原失败，再运行受影响 lane；报告同时保留首次失败和最终结果。

## 门禁与交付

阻断条件包括身份错配、鉴权绕过、资金/外部效果重复、数据损坏、不可恢复核心失败、候选与证据 SHA 不一致、必测项未执行或缺少证据、迁移/恢复演练失败，以及未关闭的必测 skip。

交付包括：本 PRD、`report-template.md`、coverage inventory（由独立负责人维护）、quality lanes 的原始输出、每 lane 的环境收据、缺陷台账、白名单 Provider receipt 与部署后 readback。最终报告只能给出“允许上线”或“禁止上线”及对应证据，不能以环境可行性报告替代验收结论。
