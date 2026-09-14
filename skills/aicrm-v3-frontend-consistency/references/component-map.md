# 前端真实组件索引（本次分销能力补充）

以当前仓库路径为准。索引用于选择现有入口，不授权跨领域读取或写入；动工前除路径外，还要追踪 canonical 路由、handler／adapter、`Render*` 或挂载、assets 和页面调用。未挂载实现与历史快照不作为当前标准。

| 场景 | 入口 | 调用／装配 | 边界 |
| --- | --- | --- | --- |
| 管理端单壳 | `internal/webshell/templates/admin_base.html` | `internal/webshell/renderer.go` 的 `RenderDistribution` | 仅一个 `.admin-sidebar` 和 `.admin-topbar`；分销领域只提供经 manifest 验证的正文 assets。 |
| 问卷管理与编辑器 | `web/v3/surveyAdapter.ts`、`web/src/admin/sections/questionnaireEditor.ts` | `internal/survey/ui.go` 解析 manifest，`RenderSurvey` 在冻结 admin bundle 前装配列表 bridge；编辑器由 `questionnaireEditor` 入口挂载 | 一级列表复用冻结 table、检索和操作；bridge 仅映射问卷名称。编辑页保留标题用于答题/预览，状态以服务端 readback 为准。 |
| 通用详情抽屉 | `web/v3/shared/ui/detailDrawer.ts`、`web/v3/shared/ui/detailDrawer.css` | `distributionAdmin.ts` 导入行为，`sharedDetailDrawerStyles` 由 `admin_base` 挂载 | 组件只负责焦点回收、Escape/关闭和视觉容器；调用方必须读取自己的受权服务端事实，不能由抽屉伪造状态。 |
| 商品编辑分销配置与申请入口 | `web/v3/productAdapter.ts`、`web/v3/productDistribution.css` | Product UI 的 `ProductCSS` 经 `admin_base` 装配 | 沿用商品编辑表单 token 和公开申请 URL／二维码，不新增壳、配色或身份／资金模型。 |
| 分销管理详情 | `web/v3/distributionAdmin.ts`、`web/v3/distribution.css` | `RenderDistribution` 的 `#distribution-admin-root` 和 Distribution manifest assets | 只使用现有后台 table、tab、button token；公共分销中心样式必须限定在 `body[data-ui-surface="distribution"]`，不能重置后台全局样式。 |

## 全局统一 UI 增补清单（2026-09-15）

本节登记本轮实际 Host、受权读取和未完成合同。标记为 PR 的能力尚未合入 main；不能因为共享模块或组件测试存在而把未列的页面标为覆盖。商品编辑分销行由商品专项 PR 单独维护，避免与该专项并行修改。

| 场景 | 入口 | 调用／装配 | 已核实状态与边界 |
| --- | --- | --- | --- |
| 管理端全局反馈 | `web/v3/surfaceFeedbackHost.ts` | 构建脚本向生成的 admin、H5、sidebar 与分享文档注入反馈 Host | 只映射加载／资源失败表现；不把业务失败伪装成成功，也不改变领域命令。 |
| 已提交文本搜索，第一批 | `web/v3/shared/ui/committedTextSearch.ts` | 渠道：`channelCenterAdapter.ts`；标准选择器：`standardComponentsHost.ts`；其他生成页面：`surfaceFeedbackHost.ts` | **PR #287，未合入。** 只精确注册渠道、优惠券、问卷、标签、计划、素材库与标准群聊／标签／客服／素材选择器控件；不全局拦截表单。渠道真实 Chromium 输入法 Journey 已通过，其他匹配页面仍须逐页业务验收。 |
| 标签选择器 | 冻结 `wecom_tag_picker.js`；V3 `standardComponentsHost.ts` | Products、Channels、Radar、Survey 由各自 `StandardHostJS` 装配；Customers 在 release HTML 注入 stable Standard Host | 现有调用使用页面提供的标签目录／回调；不由选择器给客户打标。完整 V3 session 合同未接入。 |
| 客服／员工选择器 | 冻结 `operation_member_picker_dd8d60d.js`；V3 `standardComponentsHost.ts` | 标准 Host 加载并处理已注册的刷新动作 | 当前刷新沿用 group-ops scope 的既有调用，不可作为其他客服页面的通用范围；真实非群运营调用与 `SelectionSession` 迁移未完成。 |
| 真实群聊选择器 | 冻结 `group_chat_picker.js` | `standardComponentsHost.ts` 在 Products、Channels、Radar、Survey 等标准 Host 页面加载 | 调用方必须回传领域真实 `chat_reference`；当前仅冻结组件装配，尚无 V3 session／分页／失效回显的真实页面验收。群邀请素材命令不在此项。 |
| 素材选择器：Radar | `web/v3/shared/ui/selectionSession.ts`、`materialPickerAdapter.ts`、`radarAdapter.ts` | **PR #296，未合入。** `RenderRadar` 的 `RadarAssets.StandardHostJS` 先加载标准全局，Radar 明确注入图片／附件目录 loader 后接管可视 dialog | 实际冻结 Radar renderer callback Journey 验证单项添加；调用没有 `selectedRecords`／`onCommit`，多选移除与重新打开回显尚不是 Radar 页面验收。组件不读取 `AdminApi`、不扩大目录 scope。 |
| 素材选择器：产品及其他调用 | `web/v3/productAdapter.ts`、冻结素材 picker | `RenderProducts` 的 `ProductAssets.StandardHostJS` 与 Product Host | 当前产品仍为单项 legacy callback；不得写成已迁移 V3 material session。后续迁移须由调用方提供受权 `loadPage`、实际 `selectedRecords` 与 `onCommit`。 |
| 话术／内容编辑器 | 冻结 `send_content_composer.js` 与 V3 `standardComponentsHost.ts` | 同标准 Host 的页面加载；各领域 adapter 维持业务命令 | 尚未完成统一 V3 编辑／预览／只读合同及真实页面验收；预览不能触发发送。 |
| 经营首页与导航 | overview 的 `overviewAdmin.ts`、`navigationHost.ts`、`admin-navigation.v3.json` | **未合入。** Go webshell 与 release navigation Host 共用 JSON 数据源；资产由 overview manifest entries 装配 | 只读聚合 API 已验证；首页正文、导航和三种壳的真实浏览器验收仍未完成。 |
| 订单分销事实展示 | 交易 Order Host 与 distribution Stable Read Port | 未开始页面装配 | 详情只读取订单级快照、复核期、预计／实际结算及异常证据；不得读取商品当前比例或跨领域写入。 |

未列为「已核实」的页面不因路径相似而复用上表读取范围或命令。每次迁移更新本表时记录 canonical 路由、实际 Host／asset、数据来源和授权范围、选择回调、状态验收及尚未完成合同项。
