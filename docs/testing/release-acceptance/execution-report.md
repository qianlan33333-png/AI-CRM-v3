# 发布验收执行报告

**结论：禁止上线，验收未完成。** 本报告记录截至 2026-09-14 的候选测试证据；它不授权部署、安装、生产数据库操作或真实 Provider 写入。

## 测物与范围

| 项目 | 证据 |
| --- | --- |
| 候选提交 | `dcfc02290daca3f4bb9509120698e9e976b5369b` |
| 候选 Git tree | `0d2396be8d3ccbec9985fe0b9a5c01613d0e5ec7` |
| CI | GitHub Actions [34797031338](https://github.com/qianlan33333-png/AI-CRM-v3/actions/runs/34797031338)，`workflow_dispatch`，同一候选 SHA，completed/success |
| CI lanes | plan、preflight、backend、frontend、browser、archive-sdk、check 均 success；deploy 与 quality-report 均 skipped |
| 本地 Provider 状态 | disabled；未读取生产凭据，未调用真实 Provider |
| 迁移环境 | 独立 PostgreSQL 16.13，仅 `127.0.0.1:51011`；`aicrm_test_release_acceptance` 与 `aicrm_test_release_restore_dcfc022`；证据完成后已停止本任务实例，数据、dump 与日志保留，可显式重启用于后续隔离测试 |

CI 的成功是技术层证据，不能替代白名单业务验收、Provider receipt 或上线后的 readback。

## 隔离入口与本地 preflight

隔离入口在本次最终 preflight 中使用的 harness 提交为 `899233a2fb52008b0885fc0cbec8e0debc7baf4b`。入口只允许本地 `aicrm_test_*` 数据库，拒绝 `host`、`hostaddr`、`service`、`dbname` 等 URL 覆盖，构造最小子进程环境并强制 Provider disabled。其单元测试覆盖 URL 绕过、环境泄漏、测试源变更、脏 harness、异常 receipt、超时子进程和异常后的状态捕获。当前入口对 execute 默认限时 1800 秒、preflight 默认限时 120 秒；超时会终止整个子进程组、保留已捕获的脱敏日志，并写入 `timeout`/exit `124` receipt。SIGTERM 或交互中断会写入 `interrupted` receipt；不可捕获的 SIGKILL 可能留下 `running` receipt，不能作为通过证据。Git porcelain 仍严格阻止 tracked、staged 和 untracked 源码变化；Git 已忽略的构建缓存不在该检查范围。

最终本地 preflight 使用独立、干净的 detached harness `899233a2`，对候选运行成功。原始 receipt 为 `/private/tmp/aicrm-release-reports/dcfc022/preflight_899233a2/environment-receipt.json`，配套脱敏 stdout/stderr 位于同目录：`run_id=preflight_899233a2`、UTC `2026-09-14T02:17:01.263722Z` 至 `2026-09-14T02:17:14.922881Z`、执行模式、timeout `180` 秒、exit code `0`、候选 `dcfc022...` 与 harness `899233a2...` 均在执行后干净、`integrity_violations=[]`。它运行了候选既有 preflight 的 74 项本地检查；该成功是隔离技术证据，仍不构成业务或 Provider 验收。此前 `aa03ab97` 与 `fbacac7e` receipt 保留为历史记录，不再作为最终本地 preflight 证据。

## 隔离迁移、幂等与恢复演练

使用候选的 `cmd/migrate-platform` 构建迁移器（SHA-256 `67e01d4e968d6aa26f59f41dd6340ce82661e0d8ef9d86995d0ffefb181dc5af`），只传入本地测试数据库 URL、绝对 migrations 目录和两分钟超时。未调用 `install-release.sh`、systemd 或任何部署入口。

1. 在空的 `aicrm_test_release_acceptance` 全量执行 155 个 migrations，最高版本 `0161`，成功。
2. 对同一库以相同命令重跑，成功；migration ledger 的 count/checksum signature 为 `155:97ffb95d4d4104fc612fd09ce86994b0`。
3. 创建仅用于恢复验证的 `release_acceptance_fixture` 两行合成记录，执行 `pg_dump --format=custom --no-owner --no-privileges`，dump SHA-256 为 `833fec6e86defb9082f83dafbc35c0582069feed90371d21a43ad4b5c61e9408`。
4. 使用 `pg_restore --exit-on-error --no-owner --no-privileges` 恢复到全新 `aicrm_test_release_restore_dcfc022`。恢复库 migration ledger signature 相同；fixture 两行逐字一致。
5. 比较两库 public table/column/type/null/default、constraint identity/type/key/FK/deferrability/validation 与 index identity/key/operator/collation/predicate 的稳定 catalog signature，均为 `8596:d2c3a7c68276fafc6d3f03a907e92d78`。

原始可复核材料在 [migration-recovery-dcfc022](evidence/migration-recovery-dcfc022/)：执行命令和当时 exit 在 [execution-commands-and-results.md](evidence/migration-recovery-dcfc022/execution-commands-and-results.md)，dump SHA-256/大小/TOC 摘要在 [dump.sha256](evidence/migration-recovery-dcfc022/dump.sha256)、[dump-size.txt](evidence/migration-recovery-dcfc022/dump-size.txt) 与 [dump-list.txt](evidence/migration-recovery-dcfc022/dump-list.txt)。catalog、ledger 和 fixture 的 SQL 与两库原始输出在 [recovery-raw-outputs.tar](evidence/migration-recovery-dcfc022/recovery-raw-outputs.tar)，逐项路径、大小与 SHA-256 在 [recovery-raw-outputs.manifest.json](evidence/migration-recovery-dcfc022/recovery-raw-outputs.manifest.json)，提取步骤在 [recovery-raw-outputs.md](evidence/migration-recovery-dcfc022/recovery-raw-outputs.md)，逐项 `cmp` 结果在 [comparison-results.txt](evidence/migration-recovery-dcfc022/comparison-results.txt)。这些 SQL 输出是对保留测试库的只读复核；它们使用独立的确定性 hash 格式，不能与本节最初记录的摘要数字混用。

原始 schema-only `pg_dump` 文本不逐字相同：PostgreSQL 为每次 dump 生成不同的 `\\restrict` token，并在 restore 后重写部分逻辑表达式的括号/位置。该文本差异未作为结构一致性通过依据；catalog 结构签名、migration ledger 与合成数据恢复共同构成此次演练证据。

## 覆盖状态与未关闭门禁

coverage inventory 已提交，但它是静态盘点，不是执行通过证据。

CI browser lane 的历史 artifact 显示 14 个 journey 均通过，但 `eligible_for_delivery=false`。这是 phase 设计的结果，不能作为严格干净的 release proof；其 `source.unchanged_during_execution=false` 来自运行时把 admin-shell-layout 与 sidebar-standard 截图写进冻结 donor/worktree 的未跟踪路径，HEAD 与 tree 未变。历史 receipt 保持原样。隔离入口现已把 `AICRM_ADMIN_LAYOUT_SCREENSHOT_DIR` 和 `AICRM_SIDEBAR_SCREENSHOT_DIR` 显式指向本次 run 的外部 artifacts 目录，未来 browser lane 不会因这两类截图污染测试源；其余未跟踪变化仍严格阻断。

以下任何一项未关闭前，结论保持禁止上线：

- 指定白名单身份的企微、OAuth/H5、支付/退款真实闭环，含 Provider receipt、CRM 回读和业务确认；当前没有可用白名单凭据或回执。
- 至少 24 小时稳定性与按实际峰值定义的容量/并发测试；尚未执行。
- 新库迁移、备份恢复之外的真实发布包构建、目标主机升级、服务切换、回退兼容与上线后认证 readback；用户未授权部署，尚未执行。
- 每个必测 skip 的同候选 SHA 闭合证据；任何未关闭必测 skip 均阻断。
- 候选本身的 route 合同问题和 HTTP 证据缺口，详见 [route-gap-triage.md](route-gap-triage.md)：退款对账 `POST /api/admin/wechat-pay/refunds/{refund_id}/reconcile` 已有 handler 分支但 Composition Root 没有挂载该前缀，当前候选无法作为该 HTTP 合同通过；修复在进行中，但未纳入本候选验收。OpenAPI 的 `/api/ai/audience/packages/{package_key}/webhook` 与实际挂载的 canonical integrations webhook 路径不一致，须先确定对外合同再修正文档或实现。另有 9 个已挂载路径以及 acquisition PATCH/DELETE 缺少 method+path 的 HTTP contract 测试，不能以服务层、存储层或通用鉴权测试闭合。

本报告不将本机 macOS 缺少 Linux Chrome/archive SDK 计为业务失败；对应技术 lane 已由同候选 SHA 的 CI 运行通过。它同样不把 CI 或本地 lane 的成功推导为外部业务效果成功。
