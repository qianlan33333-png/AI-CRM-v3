# Migrations

v3 从新的 Schema 基线开始，不复制 production 或 V2 的完整 migration 历史。每个领域迁移必须声明表 Owner、数据保留、回滚或 forward-only 策略，并通过真实 PostgreSQL 测试。

- `0063_identity_hxc_source_observations.sql`：Identity 所有的 HXC 主体、加密观察、不可变解析收据和人工冲突动作；forward-only，不自动建客或合并。
- `0064_hxc_dashboard_identity_v2.sql`：HXC 投影的双键匹配来源、原因码、Case/候选关联和 inspect/apply 运行模式；forward-only，升级时保留上一成功投影。
- `0065_channel_legacy_asset_retirement.sql`：Channel 旧资产在被新资产替换后保留已验证事实并允许标记退役；forward-only，不改变 Provider 验证状态。
- `0086_wecom_profile_primary_owner.sql`：WeCom 完整目录同步后从受信 follow 集合恢复主负责人事实；空集合保留旧值，旧存量在下一次完整同步前保持 unknown。
