# 共享视觉变量与组件状态示例页

## 业务判断

- **OneID：不涉及。** 页面只展示本地明确写死的示例群聊与素材记录，不解析、创建或关联客户／外部身份。
- **持久化：不涉及。** 所有状态和选择都只保留在浏览器当前会话，确认动作只更新示例文本，不请求业务命令、数据库或队列。
- **Provider／外部效果：不涉及。** 不读取或写入 Provider；页面明确标注“示例数据，不会发送、保存或调用 Provider”。

## 依据与范围

- 复用 [PR #296](https://github.com/qianlan33333-png/AI-CRM-v3/pull/296) 的 `SelectionSession`／素材适配器提交边界，以及 [PR #300](https://github.com/qianlan33333-png/AI-CRM-v3/pull/300) 的 Owner 范围群聊选择契约；本页不把它们的业务读写扩大到其他领域。
- 视觉沿用 `artifact-template-crm` 的浅灰画布、白色内容卡、蓝色主操作、清晰行距和圆角状态。冻结 donor 不修改。
- 新增 V3 `sharedVisualTokens` 只做兼容映射：颜色、面板、边框、状态和圆角继续以 `admin_console.css` 的 `--brand`、`--text`、`--bg`、`--panel`、`--line`、`--ok`、`--warn`、`--danger`、`--radius-*` 为权威，字号和控件高度继续以 `presentation.css` 的 `--ui-*` 为权威。映射层只提供 V3 dialog 的稳定语义 alias 和 fallback，加载在冻结 admin shell 与 `presentation.css` 之后，不覆盖 donor 或业务预览的既有变量。

## 用户可见行为

`/admin/component-states` 仅在既有后台管理员会话内可访问。页面展示 loading、empty、error、forbidden、readonly、invalid selected 与 IME 安全搜索状态，并显示各状态的含义和可恢复动作。

页面的“群聊示例”调用真实 `openGroupPicker`，“素材示例”调用真实 `installMaterialPickerAdapter`／`SelectionSession`／dialog；目录 loader 只返回显式示例数据。表单选择器示例使用同一个 `installSelectionDialog`，包含 `select`、`textarea` 和 `contenteditable`，以验证焦点陷阱和 Escape 行为。确认只更新页面内的示例结果。

## 交付与验收

- `admin_base`、manifest、release stage 和安装校验形成同一资源闭包；没有匿名公开入口或导航重复壳。共享变量仅先覆盖 V3 selection dialog 与本示例 Host，其他业务页需分别接入后再宣称覆盖。
- 针对示例 Host 的状态／IME／焦点测试，以及现有 material/group session 测试通过。
- 认证 Chromium 在 `1280`、`1440`、`360`、`420` 记录页面与弹窗截图；代表性 Radar／GroupOps Host 仍加载共享样式且不回退。
