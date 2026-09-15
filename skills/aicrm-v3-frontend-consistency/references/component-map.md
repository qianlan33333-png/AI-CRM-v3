# 前端真实组件索引（本次分销能力补充）

以当前仓库路径为准。索引用于选择现有入口，不授权跨领域读取或写入；动工前除路径外，还要追踪 canonical 路由、handler／adapter、`Render*` 或挂载、assets 和页面调用。未挂载实现与历史快照不作为当前标准。

| 场景 | 入口 | 调用／装配 | 边界 |
| --- | --- | --- | --- |
| 管理端单壳 | `internal/webshell/templates/admin_base.html` | `internal/webshell/renderer.go` 的 `RenderDistribution` | 仅一个 `.admin-sidebar` 和 `.admin-topbar`；分销领域只提供经 manifest 验证的正文 assets。 |
| 企微客户侧边栏 | `web/v3/sidebar/main.ts`、`web/v3/sidebar/presentation.css` | `RenderSidebar` → sidebar manifest assets → `sidebar_workbench_v3_overlay.js` | 仅复用签名 context 与 `SidebarBridge`；不挂员工、群、标签 picker 或自动化话术目录。403／context 失效清空客户缓存和操作入口；素材标签来自 Media Owner。 |
| 问卷管理与编辑器 | `web/v3/surveyAdapter.ts`、`web/src/admin/sections/questionnaireEditor.ts` | `internal/survey/ui.go` 解析 manifest，`RenderSurvey` 在冻结 admin bundle 前装配列表 bridge；编辑器由 `questionnaireEditor` 入口挂载 | 一级列表复用冻结 table、检索和操作；bridge 仅映射问卷名称。编辑页保留标题用于答题/预览，状态以服务端 readback 为准。 |
| 通用详情抽屉 | `web/v3/shared/ui/detailDrawer.ts`、`web/v3/shared/ui/detailDrawer.css` | `distributionAdmin.ts` 导入行为，`sharedDetailDrawerStyles` 由 `admin_base` 挂载 | 组件只负责焦点回收、Escape/关闭和视觉容器；调用方必须读取自己的受权服务端事实，不能由抽屉伪造状态。 |
| 顶栏动态操作 | `web/v3/shared/ui/pageHeaderActions.ts` | 挂载既有 `.admin-topbar > .admin-topbar-meta`；`distributionAdmin.ts` 首个调用，图片库等 V3 Host 可复用 | SSR `PageAction` 仅承载链接；此组件只挂载已由调用页授权的客户端操作，不创建第二标题栏或业务命令。 |
| 商品编辑分销配置 | `web/v3/productAdapter.ts`、`web/v3/productDistribution.css` | Product UI 的 `ProductCSS` 经 `admin_base` 装配 | 仅“售卖信息”承载启用分销开关、本商品佣金比例与退款复核等待天数；其他维度不写 `distribution_policy`，商品编辑页不提供申请链接、复制或二维码。公共分销中心保持其既有入口；不新增壳、配色、身份或资金模型。 |
| 分销管理详情 | `web/v3/distributionAdmin.ts`、`web/v3/distribution.css` | `RenderDistribution` 的 `#distribution-admin-root` 和 Distribution manifest assets | 只使用现有后台 table、tab、button token；公共分销中心样式必须限定在 `body[data-ui-surface="distribution"]`，不能重置后台全局样式。 |
