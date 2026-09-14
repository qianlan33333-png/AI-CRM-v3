SELECT count(*) || ':' || md5(COALESCE(string_agg(version || ':' || checksum, ',' ORDER BY version), '')) AS migration_ledger_signature
FROM platform_schema_migrations;
