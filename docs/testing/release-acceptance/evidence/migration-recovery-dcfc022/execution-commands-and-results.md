# 迁移恢复原始执行索引

此目录保存 2026-09-14 的本机隔离恢复演练证据。所有数据库均为本任务 PostgreSQL 16 实例 `127.0.0.1:51011` 中新建的 `aicrm_test_*` 库；命令未读取生产凭据。退出码为执行当时记录的结果；本目录中的 SQL 输出与 dump 清单是随后只读复核得到的原始可检查材料。

| 步骤 | 命令（变量已展开为无凭据值） | exit |
| --- | --- | ---: |
| 构建候选迁移器 | `GOCACHE=/private/tmp/aicrm-migration-dcfc022/go-build-cache GOMODCACHE=/private/tmp/aicrm-migration-dcfc022/go-mod-cache go build -o /private/tmp/aicrm-migration-dcfc022/migrate-platform ./cmd/migrate-platform`（cwd `/private/tmp/aicrm-release-test-source-dcfc022`） | 0 |
| 空库迁移 | `AICRM_DATABASE_URL=postgresql://aicrm_test@127.0.0.1:51011/aicrm_test_release_acceptance?sslmode=disable /private/tmp/aicrm-migration-dcfc022/migrate-platform -dir /private/tmp/aicrm-release-test-source-dcfc022/migrations -timeout 2m` | 0 |
| 幂等重跑 | 同上一条 | 0 |
| dump | `pg_dump --format=custom --no-owner --no-privileges --file /private/tmp/aicrm-migration-dcfc022/aicrm_test_release_acceptance.dump postgresql://aicrm_test@127.0.0.1:51011/aicrm_test_release_acceptance?sslmode=disable` | 0 |
| 空恢复库 | `createdb -h 127.0.0.1 -p 51011 -U aicrm_test aicrm_test_release_restore_dcfc022` | 0 |
| restore | `pg_restore --exit-on-error --no-owner --no-privileges --dbname postgresql://aicrm_test@127.0.0.1:51011/aicrm_test_release_restore_dcfc022?sslmode=disable /private/tmp/aicrm-migration-dcfc022/aicrm_test_release_acceptance.dump` | 0 |
| ledger/catelog/fixture 双库比较 | `psql ... -Atf migration-ledger-signature.sql`; `psql ... -Atf catalog-signature.sql`; `psql ... -Atf fixture-compare.sql`; then `cmp` each corresponding output | 0 |

候选迁移器 SHA-256：`67e01d4e968d6aa26f59f41dd6340ce82661e0d8ef9d86995d0ffefb181dc5af`。

`catalog-signature.sql` 以数据库目录为准，覆盖 public tables 的列、constraint 与 index 定义。两个数据库的当前只读复核输出在对应 `*.catalog.txt` 中相同；该 SQL 的哈希格式独立于报告中最初的 catalog 摘要格式，不能把两种格式的数字直接混用。
