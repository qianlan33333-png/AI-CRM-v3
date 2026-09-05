# Migrations

v3 从新的 Schema 基线开始，不复制 production 或 V2 的完整 migration 历史。每个领域迁移必须声明表 Owner、数据保留、回滚或 forward-only 策略，并通过真实 PostgreSQL 测试。

- `0063_identity_hxc_source_observations.sql`：Identity 所有的 HXC 主体、加密观察、不可变解析收据和人工冲突动作；forward-only，不自动建客或合并。
- `0064_hxc_dashboard_identity_v2.sql`：HXC 投影的双键匹配来源、原因码、Case/候选关联和 inspect/apply 运行模式；forward-only，升级时保留上一成功投影。
- `0065_channel_legacy_asset_retirement.sql`：Channel 旧资产在被新资产替换后保留已验证事实并允许标记退役；forward-only，不改变 Provider 验证状态。
- `0081_group_ops_webhook_unconfigured_reference.sql`：Group Ops 未配置 webhook 的空 reference 可由多个本地计划共享；非空 opaque reference 仍全局唯一；forward-only，不变更任何已配置 webhook。
- `0082_group_ops_history_import.sql`：Group Ops 的四类 V1 只读历史投影导入批次与逐行摘要/隔离收据；保留文本来源员工和群标识，不创建当前计划、任务或外部效果；forward-only。
