# V3 标准选择组件覆盖账本

分类结论：OneID 不参与选择控件；读取目录属于受权限约束的 Provider/本地目录读取。商品配置在商品 Owner 的单一事务保存；客户打标签仍经既有 Customer TagCommand/External Effects 边界，组件不新增 Provider 写入、身份匹配或队列。

| 入口 | 实体与标准原件 | 外层 Host / 权限与保存映射 | 验证状态 |
| --- | --- | --- | --- |
| 客户目录与档案（`customers.html`、`customerDetail.html`） | 客服 `OperationMemberPicker`；标签 `AICRMWeComTagPicker` | `customerAdapter` 在发布页面隐藏旧 ID 输入。负责人仅以 `scope=owner_migration` 返回的 `staff_id` 保存；预检 403 时隐藏控件，不扩大仅超级管理员的目录权限。标签为可信正整数 `tag_id`。 | 原件 DOM 选择、查询参数和 `staff_id`/`user_id` 分离已测。 |
| 客户静态目录批量标签 | `AICRMWeComTagPicker` | `admin_customers.js` 保持 Customer TagCommand 预览/确认流程；标准 picker 只填原有选择框。 | 浏览器旅程已测。 |
| 普通商品、周期商品 | 页面图片 `AICRMMaterialPicker`；标签 `AICRMWeComTagPicker` | `productAdapter` 将普通/周期表单各自的“从素材库选择”延迟旧 picker 接到同一原素材 picker，再回填冻结草稿/保存通道；标签保存为 `enabled:boolean, tag_ids:number[]`。购买后动作由 Host 控制 `purchase_action_enabled` 与 `qr|redirect`，未启用或非当前模式字段会在写入前清空。 | 普通商品覆盖标签启用/关闭、跨页素材、保存 payload、重复保存恢复；周期商品冻结表单覆盖跨页素材、取消不改草稿和保存 payload。 |
| 周期商品会员表 | `OperationMemberPicker` | `member_grid_host` 仅把原件读取映射到产品已有 Access-scoped staff 目录，保留 `disabledUserIds`、上限与无全局刷新。 | Go + jsdom 原件旅程已测。 |
| 内容雷达 | `AICRMMaterialPicker` | `radarAdapter` 仅适配真实图片/附件目录的分页、搜索与原始素材 ID；冻结 `radar.ts` 未修改。 | 原件素材 DOM relay（真实目录项回填冻结保存状态）已测。 |
| 问卷编辑（选项、评分、评估标签） | `AICRMWeComTagPicker` | 所有可达标签按钮均由 `mountTagPicker` 汇入唯一 `openTagModal`，调用原件 `AICRMWeComTagPicker.open`；标准 Host 在原标签全局加载后锁定该全局，冻结编辑器无法覆盖它。编辑器没有素材、群聊或客服选择动作。 | 路径与加载顺序已复核；逐入口选择和保存旅程仍由问卷验收覆盖。 |
| 群运营、AI 助手 | 客服、群聊、素材、话术 composer 原件 | 现有 GroupOps / AI Host 的限定目录和保存契约不变。`agentEdit.html` 的固定素材区域为 API 明确不支持写入的只读展示，未添加虚假的选择器。 | 已有各自 Host 旅程；本账本只统一发布出口。 |
| 渠道 | 客服、标签、素材、composer 原件及渠道原表单 | `channelCenterAdapter` 由渠道 Owner 维护；共享发布清单提供原件和被动资产。 | 渠道 Host 旅程已测。 |
| 优惠券 | 优惠券原表单及抽取的同字节 runtime | CSP 使用 `/assets/standard-components/coupon_form_runtime.js`，由页面 Host 在挂载原 DOM 后加载。 | Coupon CSP/失败重试旅程由渠道 Owner 维护。 |

没有选择动作的标签管理列表、只读展示和历史数据不替换控件。`agentEdit.html` 的固定素材区域是只读的：现有 API 把四个素材 ID 数组限制为 `maxItems=0`，没有可保存的选择动作。旧 `web/src/shared/ui/picker.ts` 中仍有冻结 controller 的历史方法；每次发布须按实际服务模板和路由确认可达性，不能凭全局组件加载把它们计为覆盖。
