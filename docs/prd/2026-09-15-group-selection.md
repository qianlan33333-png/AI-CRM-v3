# 群聊共享选择器与群运营计划接入

## 目标

在群运营计划详情的“选择群”入口接入 V3 共享选择器。运营人员可按群聊目录选择、取消或移除多个目标群；提交时由既有 GroupOps 计划群资产命令保存完整差异，重新打开时以服务端绑定结果回显。

## 边界与分类

- **OneID：不涉及。** 选择值是 GroupOps Owner 的不透明 `chat_reference`，不读取、解析或创建客户、外部身份。
- **持久化：选择器不涉及。** 它只保留页面会话草稿；确认回调由现有 GroupOps `plans/{id}/groups` 命令按既有 revision 写计划群资产。没有新表、队列或事务。
- **Provider 与外部效果：不涉及。** 目录由已授权的 GroupOps 本地目录投影读取；本能力不触发同步、入群、消息发送、素材上传或 Provider 调用。

## 交互合同

调用方显式提供 `source`、`scope`、带 query/cursor/signal 的分页读、初始完整 `selectedRecords`，以及一次完整结果的 `onCommit`。选择器支持多选、readonly、不可选原因、搜索按钮或非 IME Enter、分页选中保持、刷新失败保留草稿、乱序读保护、取消无写入、焦点返回和键盘操作。已绑定但目录缺失的群不静默丢弃，显示为不可用的历史绑定。

## 实际接入与参考

- 接入页：`web/v3/groupOpsHostAdapter.ts` 的群运营计划详情“选择群”；复用 `SelectionSession` 与既有 `group-ops` modal 样式，不修改冻结 `web/v3/groupOpsStandard.js`。
- 领域读取/写入：`internal/groupops/http/handler.go`、`internal/groupops/app/runtime.go`、计划群资产端点。
- 既有共享选择器参考：`web/v3/shared/ui/materialPickerAdapter.ts` 与 `SelectionSession`。
- 本仓现有 GroupOps Host 端到端参考：`scripts/groupops-host-adapter-e2e.mjs`。

本 PR 仅迁移此一真实 GroupOps 调用点；素材、成员、标签和客服页面不在范围内。
