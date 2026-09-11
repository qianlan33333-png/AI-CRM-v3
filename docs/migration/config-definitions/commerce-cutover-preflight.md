# 当前商品与券定义的切流预检

分类：不涉及 OneID（仅定义，不包含客户/领券行）；持久化为源和目标
repeatable-read/read-only 事务；不涉及 Provider、任务或外部效果。

使用显式 `--commerce-only`，只支持 extract、inspect、dry-run。
它不会抽取群计划或 Agent，数量来自本次 manifest，不要求旧 31/15 基线。
旧命令默认行为、旧加密文件及旧行摘要保持不变。

```sh
# DSN 只经受保护环境提供，禁止回显。源读 AICRM_SOURCE_DATABASE_URL。
bin/migrate-v2-config-definitions --commerce-only --mode=extract \
  --source-revision=<source-git-sha> --snapshot=/secure/commerce.enc \
  --snapshot-key-file=/secure/snapshot.key
bin/migrate-v2-config-definitions --commerce-only --mode=inspect \
  --snapshot=/secure/commerce.enc --snapshot-key-file=/secure/snapshot.key
# 目标读 AICRM_DATABASE_URL；本命令仍是数据库 READ ONLY。
bin/migrate-v2-config-definitions --commerce-only --mode=dry-run \
  --snapshot=/secure/commerce.enc --snapshot-key-file=/secure/snapshot.key \
  --manifest-sha256=<inspect-digest> --actor-admin-user-id=<active-admin>
```

加密文件和密钥均为 0600，输出文件存在即拒绝。新 scope 和券 public_slug、
issued_count 进入认证加密快照与摘要；旧 scope 不接受新券字段，新 scope
强制每张券都有这两项（零和空字符串也是显式事实），禁止静默缺省。
业务定义验证、引用完整性和 manifest 自洽检查沿用现有验证器。

dry-run 报告按已有 source_system/source_kind/source_key 查询映射：

- mapped_source_equal：旧源摘要一致，可以复用既有 target_id，不新增重复定义。
- new_source：没有旧映射；商品 code 还须未被占用。仅表示候选新增，不代表已新增。
- conflict_source_drift：源旧行已变，禁止覆盖。
- conflict_target_edited：目标版本大于 1，有运营改动，禁止覆盖。
- conflict_unmapped_product_code / conflict_target_missing / conflict_target_owner：需核查。

历史 Coupon 源摘要比较剔除旧快照未捕获的两个新字段，维持旧 source map 兼容。
这些新事实仍在完整快照里保留，不宣称旧目标已匹配。即使所有行 mapped，
仍需 Owner 级完整投影核验，因此 apply_ready **始终为 false**。

## 明确剩余能力

Coupon DefinitionImporter 当前拒绝非零 IssuedCount，INSERT 固定 issued_count=0，
不导入 public_slug。现有 Product/Coupon 导入器是 insert-only，没有带源摘要和
目标版本校验的更新契约。不能通过绕过 Owner、改源键或抹零数量完成切流。
故 commerce-only apply/verify 在打开目标数据库前明确拒绝，尚未实现新商品写入。
后续需 Owner 提供切流导入契约，核对旧 source map 与目标字段，再将本预检候选
转为同一 UoW 中可重放的新增/受控更新。不得将成功 dry-run 当作迁移完成。

源/目标实际 schema 或数量漂移必须作为事实报告。本实现没有猜测今天 delta，
没有连接生产或修改任何生产数据。
