# 核心产品配置视觉验收

- source visual truth: `/Users/qianlan/.codex/skills/artifact-template-crm/assets/reference.png`，2342×1578；生成的选定方向 `/Users/qianlan/.codex/generated_images/01a09642-d877-7ae2-a9a7-3e245901b6f9/exec-cac37bab-a5d3-4e91-b2f8-ef5193c17924.png`，1505×1045。
- 实现截图：`/tmp/core-chinese-ui-shots/core-step-1-1440.png`、`core-step-2-1440.png`、`core-step-3-1440.png`、`audience-create-chinese.png`、`core-product-edit-390.png`。
- viewport: 1440×900 和390×900 CSS px，deviceScaleFactor=1，截图与CSS尺寸一致。
- 状态：真实Host登录后的1个已保存测试产品、已发布版本1；无模型外部调用。参考图2个示例产品，不对示例数量做像素对齐。保留现有CRM壳、侧栏品牌和字号，不引入参考图虚构品牌/账号。

## 对照与修复历史
1. 首轮1440截图：结构符合三个步骤、表格和右侧主操作目标。窄屏真实浏览器发现既有人群包网格被表格最小宽度撑开（P2），测试拒绝通过。
2. 为本页原有人群包卡片添加min-width:0，使表格在自身滚动容器内滚动。重跑完整本能力Chromium旅程，1440/390三个步骤及编辑弹窗均通过，无页面横向溢出、无运行时异常。
3. 同一个图像对照输入中并列查看生成方向、真实桌面产品页和中文创建弹窗；另检查规则、验证页和窄屏编辑表单。参考与实现在内容行数、已发布通知条、原有分组列上有意不同，不作截图克隆。

## 必查视觉面
- 字体：沿用管理端中文无衬线字体，标题20/17px、正文13–14px，层级清楚；表头加强到#475467，避免过淡。
- 间距：模块统一24px内边距、16px内容间距；单产品弹窗取代五个长表单。移动端16px内边距。
- 颜色：浅灰背景、白色面板、蓝色操作和当前步骤、浅蓝提示。保留原有组件圆角而非重做全局壳。
- 图像：内容为原生表单和表格，无装饰资产替代；参考PNG未修改。生成图仅为设计参考，未以整图覆盖真实交互。
- 文案：模板中文名称与说明、草稿/发布版本、是否入包、异步待处理等均明确。用户自定义名称不翻译；内部键继续用于API。

## 验收
真实产品保存与回读、规则发布回读、绑定不可改、9种模板无英文内部键、三步导航、桌面和窄屏、控制台运行时异常检查通过。专项DOM验证覆盖草稿保留、预览不入包、正式分配确认、重复客户编号合并。真实模型推荐不在视觉验收中宣称完成。

final result: passed

## 最新用户调整：独立页签（替代上下堆叠）
- 用户明确否定参考图中的上下组合布局；改用素材库 `mountMaterialLibraryTabs` 的同页顶部切换规则与蓝色选中样式。最初生成图的下半部不再作为布局目标。
- 最新实现证据：`/tmp/core-chinese-tabs-shots/core-step-1-1440.png` 和 `/tmp/core-chinese-tabs-shots/audience-packages-1440.png` 在同一图像输入中检查；分别为仅产品、仅人群包两个互斥状态。字体、白色面板、蓝色选中态沿用模板；正文不重复铺开。
- 1440×900与390×900、1倍像素截图和真实Host测试重新完成。产品保存、规则发布、页签互斥及无横向溢出通过；DOM验证切换保留草稿、URL状态及浏览器历史恢复。
- 最终交付使用上述最新截图；旧截图仅保留为被替换布局的历史证据。
- final result: passed

## Referral activity detail

- Evidence: `/tmp/referral-dashboard-review/admin-1440.png`, `admin-detail-1440.png`, `admin-teams-1440.png`, `admin-member-drilldown-1440.png`, and `admin-captain-entry-1440.png` from the real composed Host and isolated PostgreSQL fixture.
- The CRM shell, activity context, lifecycle/date, data-first metrics, compact daily list, teams, members, named member drilldown, and captain-owned QR remain visible without leaving the admin experience.
- The Host journey verifies 53 trusted-session participations, pagination, team filtering, scoped direct-invite export, reversal, manual reward review, and the QR's same-origin `/referral?campaign=…` payload. The designated captain must still complete trusted login and explicit participation.

final result: passed
