# Global UI 执行状态

更新时间：2026-09-15
本文只记录已核实的分支、测试和宿主接入；未合入 main、未发布和未在真实业务页验收的状态分别保留。

| 阶段 | Owner | 状态 | commit | 证据 |
| --- | --- | --- | --- | --- |
| 当前 main 基线 | root | 已核实 | `9de7c37312f08e531ecaf1956b874a9e5c745511` | 当前 main；较路由盘点历史基线 `3eda04cbd56d7bfdf44ba2a15d573ee29703926a` 新增 PR #302 的 Excel 分页、PR #288 的 Channel Center 稳定 ID archive 与 Excel 分页 quality lane，不重做矩阵条目。 |
| 独立工作树 | `/root/template_packaging` | 已完成 | `9ef314a5` | `git worktree` 创建成功，工作树从 `origin/main` 建立 |
| PRD 与前端 Skill | `/root/template_packaging` | 已完成 | `9ef314a5` | `PRD.md`、`SKILL.md` 已落盘；main 现有 `references/component-map.md` 保留 |
| Product Design PNG 模板 | `/root/template_packaging` | 已完成 | 不适用（个人技能） | `artifact-template-crm`；`/Users/qianlan/.codex/skills/artifact-template-crm/artifact-template.json`；reference/preview SHA-256=`84f24c0c9d43b624db1a652599d27d42b18b9b046c74cdac4ff77f059b4bfce4` |
| Enter／输入法搜索（第一批） | terra | Ready for review，未合入 | `9c05b9d2`；[PR #287](https://github.com/qianlan33333-png/AI-CRM-v3/pull/287) | Linux frontend、browser、backend 和 quality report 全部通过（run `34876725254`）；真实 PostgreSQL／认证 Chromium 渠道 Journey 验证候选 Enter 不过滤，随后普通 Enter 仅更新一次本地已加载列表且焦点／选区保留。未部署，不称全局搜索完成。 |
| V3 素材选择会话与 Radar 接入 | terra | Draft，未合入 | `096779ce910c58ce99b8ed9ee1282e774fde3308`；[PR #296](https://github.com/qianlan33333-png/AI-CRM-v3/pull/296) | 最终冻结头的 `SelectionSession`、素材适配器状态测试及 Radar renderer + legacy callback Journey 已通过；共享适配器现已在 awaited caller 成功后提交，in-flight 重复确认锁定，失败保留 draft 并显示错误，缩略图失败显示“预览暂不可用”。截图只证明共享弹窗布局。Radar 当前仍是单项 `onConfirm`、空初选，完整多选移除／重开回显和业务保存回读尚未验收。 |
| V3 群聊选择会话与 GroupOps 接入 | terra | Ready for review，未合入 | `b5cc35814b6b2c29899ae727a9c9c646091faa9c`；[PR #300](https://github.com/qianlan33333-png/AI-CRM-v3/pull/300) | main 继续冻结既有群聊组件；PR #300 在真实 GroupOps 接入 V3 session，保留 `chat_reference`、Owner scope 分页、403 只读、保存锁、部分失败草稿保留及保存后读回，并新增 renderer 刷新后的 action ready 等待。旧 CI browser run `34889971717` 失败，不能按通过处理；新 CI `34892980966` 的 plan/preflight/frontend/browser/backend/archive/check 全通过，deploy/quality-report skipped；未部署。视觉参考在 `/private/tmp/aicrm-pr300-group-selection-chrome-evidence/`。 |
| V3 标准组件合同与统一视觉 | terra | 进行中 | 待定 | 标签、真实群聊、客服、话术／内容、公共预览与只读详情尚未完成相同的真实页面接入；冻结 donor 不修改。组件目录列出当前调用与缺口。 |
| 商品售卖信息内分销配置 | product owner | 已实现，独立验收待完整 CI／发布 | `1f11e10bb6c5cde47e4732f38ecffab6708be1aa`；PR #291 | 售卖信息独占分销块，保留启用开关、佣金比例和退款复核等待天数；商品编辑页不再放分销员申请入口、链接或二维码。 |
| 交易订单分销展示 | order/distribution owner | CI frontend 失败，未验收 | 当前 PR #301 `6e8279` | 订单页浏览器已有 1280 成功、1440 outcome-unknown 截图；同口径 overview 指标下钻仍未实现。需先修复 CI frontend，再以订单快照、复核期、预计／实际结算／分账和异常证据驱动 UI；文案使用“系统分账成功确认时间”，不代称银行到账。 |
| 经营首页只读聚合 API | overview owner | Ready for review，未合入 | `de567042885a364adc36037b20fa733ea8a02e82`；[PR #293](https://github.com/qianlan33333-png/AI-CRM-v3/pull/293) | 只读聚合 API 的领域、HTTP 与 PostgreSQL 契约独立于首页页面 PR 记录；尚无合入、部署或生产读回证据。 |
| 经营首页正文与 V3 导航 | overview owner | Ready for review，未合入 | `faa906b600517ca983af256900c8d91f9396f14b`；[PR #299](https://github.com/qianlan33333-png/AI-CRM-v3/pull/299) | CI `34888998544` 的 plan/preflight/frontend/browser/archive/backend/check 全 PASS；quality-report/deploy SKIPPED 符合预期。页面正文、导航和三种壳的生产浏览器读回仍未发生，不能写成已发布。 |
| 其余后台、企微 sidebar、H5／公开页 | 各领域 owner | 未完成 | 待定 | 不因首页、搜索或单一素材调用已实现而视为覆盖。各终端保持独立壳和授权边界。 |
| 共享选择器冻结审计 | luna／root | 组件定向复审通过；业务接入待验收 | 素材 `096779ce910c58ce99b8ed9ee1282e774fde3308`；群聊 `b5cc35814b6b2c29899ae727a9c9c646091faa9c` | parent 最终冻结头修复并测试了 `materialPickerAdapter.ts:286-301` 的 awaited callback、in-flight 锁、失败 draft 保留与显式重试，以及 `:238-248` 缩略图 fallback；未再发现共享组件级 P1/P2。Radar 当前仍是单项 `onConfirm` 布局桥接，无 `selectedRecords/onCommit` 业务回调；多选、移除、重开回显、真实保存／失败读回不属于本次组件证据。GroupOps 旧 CI browser run `34889971717` 失败；新 CI `34892980966` 的 plan/preflight/frontend/browser/backend/archive/check 全通过，deploy/quality-report skipped，尚无部署事实。 |
| 路由／页面条目验收矩阵（文档） | luna | 已补盘点，逐项待验收 | 基线 `3eda04cbd56d7bfdf44ba2a15d573ee29703926a` | 见 `skills/aicrm-v3-frontend-consistency/references/component-map.md` 的“路由／页面条目逐项验收矩阵”。条目数按表内连续编号核算，包含 canonical route、alias、reserved placeholder、登录／退出和 artifact；每行明确类型、Host、assets、组件、视口、状态、证据和待办，不能表述为同数量的 canonical 页面。 |

截图位于 `references/` 仅作本地视觉参考，含用户数据的 PNG 不进入 Git 提交。
