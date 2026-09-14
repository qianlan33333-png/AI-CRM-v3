# Global UI 执行状态

更新时间：2026-09-15
本文只记录已核实的分支、测试和宿主接入；未合入 main、未发布和未在真实业务页验收的状态分别保留。

| 阶段 | Owner | 状态 | commit | 证据 |
| --- | --- | --- | --- | --- |
| 当前 main 基线 | root | 已核实 | `f13f65b79659309555a0653321f7c52a02893270` | PR #290 小程序素材前端校验；与素材选择会话范围不重叠。 |
| 独立工作树 | `/root/template_packaging` | 已完成 | `9ef314a5` | `git worktree` 创建成功，工作树从 `origin/main` 建立 |
| PRD 与前端 Skill | `/root/template_packaging` | 已完成 | `9ef314a5` | `PRD.md`、`SKILL.md` 已落盘；main 现有 `references/component-map.md` 保留 |
| Product Design PNG 模板 | `/root/template_packaging` | 已完成 | 不适用（个人技能） | `artifact-template-crm`；`/Users/qianlan/.codex/skills/artifact-template-crm/artifact-template.json`；reference/preview SHA-256=`84f24c0c9d43b624db1a652599d27d42b18b9b046c74cdac4ff77f059b4bfce4` |
| Enter／输入法搜索（第一批） | terra | Ready for review，未合入 | `9c05b9d2`；[PR #287](https://github.com/qianlan33333-png/AI-CRM-v3/pull/287) | Linux frontend、browser、backend 和 quality report 全部通过（run `34876725254`）；真实 PostgreSQL／认证 Chromium 渠道 Journey 验证候选 Enter 不过滤，随后普通 Enter 仅更新一次本地已加载列表且焦点／选区保留。未部署，不称全局搜索完成。 |
| V3 素材选择会话与 Radar 接入 | terra | Draft，未合入 | `ec2fa833`、`9d955658`；[PR #296](https://github.com/qianlan33333-png/AI-CRM-v3/pull/296) | `SelectionSession` 及素材适配器状态测试通过；实际冻结 Radar renderer + legacy callback Journey、typecheck、build、host adapter build、shell contract 通过。与 PR #287 的提交式搜索模块组合验证通过。Radar 当前调用为单项 `onConfirm`、空初选；完整多选移除／重新打开回显只由组件合同测试覆盖，仍需一个具备 `selectedRecords + onCommit` 的业务调用页。 |
| V3 标准组件合同与统一视觉 | terra | 进行中 | 待定 | 标签、真实群聊、客服、话术／内容、公共预览与只读详情尚未完成相同的真实页面接入；冻结 donor 不修改。组件目录列出当前调用与缺口。 |
| 商品售卖信息内分销配置 | product owner | 已实现，独立验收待完整 CI／发布 | `38b86d82`；PR #291 | 售卖信息独占分销块，保留启用开关、佣金比例和退款复核等待天数；商品编辑页不再放分销员申请入口、链接或二维码。 |
| 交易订单分销展示 | order/distribution owner | 已分发，进行中 | 待定 | 待以 distribution Stable Read Port 的订单快照、复核期、预计／实际结算／分账和异常证据驱动 UI；禁止拿商品当前配置冒充成交快照。 |
| 经营首页只读聚合 API | overview owner | 已实现，未发布 | `8809aafe` | 本地 PostgreSQL、HTTP、Chromium 证据已核实；页面正文和导航的浏览器验收另列。 |
| 经营首页正文与 V3 导航 | overview owner | 开发／验收中，未合入 | `9227136d`、`3c30fa9e` | 资产与单一导航 JSON 接线已完成于首页工作树；仍需完整构建和三种壳的真实 Chromium 验收。 |
| 其余后台、企微 sidebar、H5／公开页 | 各领域 owner | 未完成 | 待定 | 不因首页、搜索或单一素材调用已实现而视为覆盖。各终端保持独立壳和授权边界。 |
| 审计与发布验收 | luna／root | 未完成 | 待定 | 需将每个合入 SHA 与适用 CI、认证浏览器 readback、真实领域数据验收及发布状态绑定；无生产请求或部署证据时不得标发布完成。 |

截图位于 `references/` 仅作本地视觉参考，含用户数据的 PNG 不进入 Git 提交。
