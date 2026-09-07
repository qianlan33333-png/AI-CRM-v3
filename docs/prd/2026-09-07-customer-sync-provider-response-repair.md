# 客户持续同步 Provider 响应兼容修复

状态：待实现；基线 `cc4bb8c7e32d4d7d89f6a9a0b0a928bb562584bc`。本文件补充 `docs/prd/2026-09-05-v3-parity/01-customer-sync.md`，不改变其验收范围。

## 分类与边界

OneID：涉及可信企微 `external_userid` 的既有显式 Provision 路径；本修复不改变身份解析、建客、合并或冲突处理。Persistence：Provider read + 已有 River 内部持久任务 + PostgreSQL UoW；不涉及企微写或 External Effects。`wecom` 继续拥有目录读取与同步轮次，复用现有 `DirectoryProvider`、`CustomerSyncService` 和 River worker，不增加队列、Worker、重试内核或跨域表访问。

## 生产证据与问题

只读查询显示：2026-09-03 的日轮次成功同步 23,461 个客户；9 月 4--7 日后续日轮次均在 `discovered_count=0` 时失败，9 月 6、7 的最终 `last_error_code` 是 `provider_response_invalid`。River 日志仅记录已脱敏的 `customer sync step scheduled for retry`，不含可用于判断字段的 Provider 原文。

只读 Provider 协议探针（不触发同步、不写入 Provider 或 PostgreSQL、不输出标识或凭据）确认员工列表有 43 个合法成员。首个 `batch/get_by_user` 页面为 57,158 bytes、100 个联系人，且每一项的 `external_contact_list[].follow_info` 都是 JSON object。当前 Adapter 把该字段声明为 slice，因此 Go 的 JSON 解码在该字段失败并统一归类为 `provider_response_invalid`，尚未进入 OneID 或客户数据写入。

## 目标与验收

1. 将单员工 `batch/get_by_user` 的 `follow_info` object 严格映射为一条关系观察；保留缺失/null 的既有零条观察语义。
2. 数组、字符串、数字及 object 内缺失/非法成员或标签字段仍返回 `provider_response_invalid`。不把协议错误解释为身份不明、隐式建客或身份降级。
3. 保持 `external_userid`、客户资料字段、成员 ID 和标签的既有验证。没有可信外部身份时，不调用 OneID Provision，也不创建客户。
4. 添加 Adapter 协议回归测试：object 成功并保留成员和标签；旧 array 表示被拒绝且保留安全错误分类。添加 Customer Sync 的持久恢复测试，证明合法 Adapter 页面可由现有任务继续提交，异常响应不会伪装为成功。
5. 跑 Adapter、wecom、真实 PostgreSQL customer-sync、race 和仓库既有 CI。生产修复后由发布负责人显式触发一轮同步；只有轮次 `succeeded` 且收据恒等式成立，才可称恢复。

## 非目标与风险控制

不修改生产配置、数据或已失败轮次；不自动重跑终态轮次。Provider 网络调用仍在 PostgreSQL 事务之外；页面提交、收据、资料投影、审计和 Outbox 继续使用既有单一 UoW。
