# 新CRM · 小鹅通式统一工作台

日期：2026-09-15  
状态：已批准，进入实现准备  
基线：`origin/main` = `e416d0c3e11703b887c55b7caf96f89e8ca55595`  
工作树：`codex/global-ui-20260915`

## 1. 业务目标与判断

新 CRM 的管理端、企微侧边栏、H5 和公共页面需要形成一套可识别、可复用的工作台体验：用户能快速看到经营事实，按一致的筛选、列表、详情和操作方式完成工作；移动端保持独立的信息层级与操作壳，不把桌面管理端压缩成窄列。

本次以用户提供的第 3 张分销完整管理页作为唯一 canonical 视觉参考。它确定浅灰工作区、白色面板、蓝色主操作、分组指标、标签页、筛选区、密集但可读的表格和明确的状态层级。其余三张图只用于补充用户详情、券码/行为数据、经营数据的内容层级，不拼图、不形成第二套视觉模板。

“小鹅通风格”在本项目中落为以下可验收的业务体验：

- 首页先给出经营概览，再进入列表和详情；指标必须有统计时间、单位和来源语义。
- 列表先筛选后操作，批量操作显示已选数量；分页、空态、加载、失败和无权限状态与真实服务端结果一致。
- 详情按“身份与基本信息 → 标签/权益 → 交易与行为 → 系统事实”分组；只显示当前员工有权读取的事实。
- 分销管理突出推广员、绑定客户、订单金额、佣金、审核和异常之间的关系；注册、链接生成和收款准备不能被显示成佣金到账。
- 话术、素材和内容页突出搜索、预览和复用；发送、群发、支付、分账等外部效果沿用已有受控入口。
- 手机端使用独立 H5/公共页壳和单列信息层级，保证标题、金额、状态和主操作完整可读。

本 PRD 只规定统一体验和读取合同，不改变佣金、支付、退款、身份归属、消息发送或自动化执行规则。

## 2. 开发前分类

```text
OneID: reads canonical customer；客户列表、用户详情、分销绑定客户只读取既有 canonical customers.id 和受权展示字段；不解析、建客、合并或自建身份主键。
Persistence: stateless/read-only UI；本次不新增业务表、迁移、前端本地事实缓存或独立队列。已有写操作继续使用所属领域的 PostgreSQL Unit of Work。
External Effects: not involved in the visual unification；本次不新增 Provider write。已有发送、支付、分账、群发和凭证签发按钮只能调用既有领域 Port/outbound 合同，并按 accepted/queued/attempted/executed/outcome_unknown/reconciled 展示服务端状态。
```

页面涉及客户、渠道身份或归属时，只经 `internal/identity/port` 或所属领域的稳定读取 Port 获取 canonical 事实；不得读取 identity/customer 表、外部 openid/UnionID/external_userid 或手机号来自行匹配。没有唯一可信证据时保留 pending/conflict，不在 UI 中猜测客户。

页面涉及可恢复动作时，UI 只提交业务意图；内部任务复用 `internal/platform/jobqueue`，企微业务写入统一由 `outbound` 协调 `internal/externaleffects/port`。本 PRD 不创建第二个 queue、worker、重试或对账状态机，也不把排队成功当作业务完成。

## 3. 只读数据汇总与分类

| 数据区域 | 本次展示的服务端事实 | 读取边界 | 本次不做 |
| --- | --- | --- | --- |
| 经营概览 | 访问、新增用户、支付金额、订单数、累计用户、累计支付和可提现/待处理金额（按现有领域合同） | 经营/订单所属 read Port，带统计时间和空态 | 不改金额、不触发提现、不把 0 当作读取失败 |
| 客户列表 | canonical 客户 ID、展示名、来源、标签摘要、最近活动和状态 | Customer read Port，服务端分页、权限过滤 | 不用外部身份字段二次匹配，不隐式建客 |
| 用户详情 | 基本资料、标签/群组、会员权益、券码、订单、学习/行为、评论反馈、系统信息 | 既有客户/权益/订单/问卷 read API；详情抽屉只负责容器与焦点 | 不暴露 token、Secret、openid、UnionID、external_userid、完整手机号或 Provider 原文 |
| 商品、订单、优惠券 | 商品/工具、价格、订单状态、券码汇总及可读操作状态 | 所属 Product/Order/Coupon adapter 与 admin_base 会话 | 不从截图或前端静态数据推导成交、退款或可用性 |
| 分销运营 | 推广员数、绑定客户数、累计订单金额、累计佣金、审核/分组、归因订单、异常与结算事实 | `internal/distribution/port` 的分页读取；`/admin/distribution` 由 `RenderDistribution` 装配 | 不改变资格、佣金、退款、收款人或分账策略；不显示“注册=到账” |
| 素材与话术 | 素材元数据、预览、话术版本、归属和状态 | Media/Automation 所属 adapter，沿用现有 manifest assets | 不发送、不群发、不调用 Provider，不在组件内持有业务写入 |
| 权限与系统 | admin_base 会话、CSRF、角色可见菜单、配置/审计的服务端状态 | 现有 Access/session 与权限 Port | 不扩大角色权限；安全配置仍只允许顶层管理员 |

所有空记录、读取失败、无权限和身份冲突都必须有区分明确的界面状态；“没有可见记录”不能由空响应推断为系统没有数据。

## 4. GitHub 参考与借鉴范围

以下仓库只作为公开交互和治理参考，不加入依赖、不复制其代码或运行时：

- [Ant Design DESIGN.md](https://github.com/ant-design/ant-design/blob/master/DESIGN.md)：借鉴设计 token、语义状态、信息层级和组件目录治理。
- [Ant Design Pro](https://github.com/ant-design/ant-design-pro)：借鉴后台工作台的信息架构、指标区、筛选表格和详情组织方式。
- [Vant](https://github.com/youzan/vant)：借鉴移动端卡片、列表、反馈和窄屏操作的独立组织方式。

参考只约束“如何让事实更清楚”，不改变 v3 的领域 Port、权限、OneID、事务或 External Effects 边界；不引入 Ant Design、React、Vant 或其他 UI 依赖。

## 5. 前端入口与复用合同

| 参考页面/入口 | 复用或扩展 | 受影响调用与边界 |
| --- | --- | --- |
| `internal/webshell/templates/admin_base.html`、`internal/webshell/renderer.go` | 唯一管理端侧栏、顶栏、会话、CSRF、页面资产装配 | 所有管理端页面；不得增加第二侧栏、donor 外壳或独立登录壳 |
| `web/v3/shared/ui/detailDrawer.ts`、`detailDrawer.css` | 详情抽屉的焦点回收、Escape/关闭、加载/失败容器 | 客户、分销员、订单、异常详情；事实仍由调用方 read Port 提供 |
| `web/v3/distributionAdmin.ts`、`distribution.css` | 分销管理页的指标、tab、筛选、表格、状态和详情入口 | `#distribution-admin-root`；公共分销中心样式限定在 `body[data-ui-surface="distribution"]` |
| `web/v3/productAdapter.ts`、`productDistribution.css` | 商品编辑表单 token、分销申请入口和可见复制回退 | 商品页与公开分销入口；不自建身份、资金或凭证模型 |
| `web/v3/surveyAdapter.ts`、`web/src/admin/sections/questionnaireEditor.ts` | 问卷列表、搜索、操作、编辑器标题和预览合同 | 保留服务端名称、状态和 readback；不让视觉层改写题目或答题事实 |
| `web/src/admin/pages/customers.ts`、`internal/webshell/static/admin_console/admin_customers.*` | 客户列表/详情既有入口与表格交互 | Customer UI 只读取 Customer/Identity 稳定 Port；PII 按权限和脱敏合同展示 |
| `skills/aicrm-v3-frontend-consistency/references/component-map.md` | 真实组件索引和新增公共组件登记 | 先配置/组合/扩展共享组件，最后才新增公共组件；不修改冻结 donor |

实际装配必须按 `canonical route → handler/领域 UI adapter → Render*/挂载入口 → manifest assets → 页面调用` 追踪。`web/dist`、未挂载历史快照和截图不能独立作为生产标准。

## 6. 实现计划

1. **基线与入口确认**：以 `e416d0c3` 建立干净工作树，逐页登记管理端、企微侧边栏、H5、公共页的 canonical 路由、handler、adapter、挂载点、manifest 和真实 read API；同步记录 OneID/持久化/外部效果分类。
2. **共享视觉合同**：在 v3-owned 公共路径统一颜色、字体、间距、卡片、指标、tab、筛选、表格、分页、抽屉、状态和无障碍焦点语义；保留现有组件默认行为，并在组件索引登记可复用扩展。
3. **管理端工作台**：以 `admin_base` 为唯一壳，依次收敛经营概览、客户列表/详情、商品/订单/优惠券、分销、素材/话术和运营页；页面只组合所属领域 adapter 的真实结果。
4. **分销 canonical 实现**：以 `references/3.png` 的布局层级作为唯一视觉目标，保留指标统计时间、tab、筛选、批量选择、分页、详情和异常语义；分销只读接口必须按所属分销员/订单服务端过滤，不能前端跨页筛选。
5. **移动端独立壳**：按 H5/公共页入口分别处理 320–430px 视口；商品/内容卡单列，主操作完整可见，必要时提供可见复制回退；不把 admin_base 或管理端数据契约搬到 H5。
6. **真实验证与分阶段交付**：先验证挂载 DOM、资产、会话、权限、加载/空态/失败和真实 HTTP 回读，再运行受影响的 typecheck/build/Journey/UI shell contract；部署、Provider receipt 和业务验收另设门禁。

## 7. 验收标准

- 桌面管理端保持一个 `admin_base` 壳；同类指标、筛选、表格、分页、抽屉、按钮和状态在各页面的 token 与交互一致。
- 客户列表/详情只显示受权 canonical 数据；Customer ID、来源、标签、权益、订单和行为的空态/错误态可区分，不能由前端猜测身份或填充示例数据。
- 分销管理具备真实指标、服务端筛选/分页、详情和异常状态；注册、推广链接、收款准备、佣金和到账语义严格分开。
- 素材、话术、订单、支付、群发和分账相关 UI 不产生新的 Provider 写调用；已有外部效果状态沿用统一生命周期并可追溯 receipt/effect_id。
- H5/公共页在 320、375、390、393、430px 视口下无横向溢出，标题、金额、状态和主操作可读；移动端不显示桌面管理壳。
- 真实浏览器验证覆盖加载、空态、失败、无权限、分页、详情关闭/焦点恢复和 HTTP readback；截图只能作为视觉对照，不能替代业务结果。
- 提交前证据绑定当前 commit/tree、干净工作树和对应测试运行；不得用旧 commit、静态成功提示或 CI 显示状态代替生产回读。

## 8. 交付记录

| 参考页面 | 复用组件 | 公共扩展/新增 | 受影响调用 | Product Design skill / 作用 / 结果 | 验收证据 |
| --- | --- | --- | --- | --- | --- |
| `references/3.png` 分销完整管理页 | `admin_base`、Distribution adapter、共享 drawer/table/token | 仅在组件缺口确认后扩展并登记 | `/admin/distribution`、分销 read API | `product-design:index` → `product-design:image-to-code`；已有视觉目标，按 canonical 复现 | 真实挂载、计算样式、分页/详情/权限 readback |
| `references/1.png`、`2.png` 用户详情与行为 | Customer 页面、详情抽屉、权益/订单/问卷 read adapter | 不新造客户详情壳 | 客户列表/详情及既有 read API | `product-design:index`；作为内容层级补充，不生成第二模板 | 脱敏、空态、失败、焦点和真实数据 |
| `references/4.png` 经营数据 | admin 指标卡、统计时间、订单/经营 read adapter | 统一指标 token | 首页/经营概览 read API | `product-design:index`；作为指标层级补充 | 统计时间、单位、来源和权限 readback |

本目录中的 PNG 仅用于本次设计审查和实现对照，含用户数据的截图不得提交到 Git。个人 Product Design PNG 模板由 Template Creator 独立写入个人技能目录，不属于本仓库交付物。
