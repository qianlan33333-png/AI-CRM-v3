# Migrations

v3 从新的 Schema 基线开始，不复制 production 或 V2 的完整 migration 历史。每个领域迁移必须声明表 Owner、数据保留、回滚或 forward-only 策略，并通过真实 PostgreSQL 测试。

- `0063_identity_hxc_source_observations.sql`：Identity 所有的 HXC 主体、加密观察、不可变解析收据和人工冲突动作；forward-only，不自动建客或合并。
- `0064_hxc_dashboard_identity_v2.sql`：HXC 投影的双键匹配来源、原因码、Case/候选关联和 inspect/apply 运行模式；forward-only，升级时保留上一成功投影。
- `0065_channel_legacy_asset_retirement.sql`：Channel 旧资产在被新资产替换后保留已验证事实并允许标记退役；forward-only，不改变 Provider 验证状态。
- `0066_channel_welcome_intents.sql`：Channel 回调欢迎语意图与首次 20 秒发送期限；复用 External Effects/River，并仅扩展既有效果作业队列约束以隔离欢迎语。
