# Migrations

v3 从新的 Schema 基线开始，不复制 production 或 V2 的完整 migration 历史。每个领域迁移必须声明表 Owner、数据保留、回滚或 forward-only 策略，并通过真实 PostgreSQL 测试。
