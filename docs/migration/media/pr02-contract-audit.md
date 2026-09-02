# PR02 素材库 donor contract audit

状态：只读供体准备；本文件是 v3 Media 的行为/接入审计，不是 HTTP 路由或数据库迁移。

## 冻结边界

- v3 base：`8ec60b7e3027e7813cd928ed43d7c8bcc7633b0e`（后续由 Root 统一 rebase 到最新 `origin/main`）。
- donor：`/tmp/aicrm-v2-audit.yN3jmr@6bfbe5816bb89913c70adaca87d6a486260e016e`，只读。
- 原样前端存放于 `web/donors/media-v2/src`；不得修改其页面、模板、TS、CSS、生成 API 或共享依赖。
- 本次后端只保留 Media-owned domain/app/port 与供体 characterization tests。没有引入 v2 module/runtime/store/worker、Customer/OneID、订单、支付、权益、Campaign、Outbound 或 Provider 调用。

## 页面入口和交互

| 页面/入口 | donor 文件 | 入口事实 | 页面交互 |
| --- | --- | --- | --- |
| `images` / 图片素材库 | `web/src/admin/templates/images.html`、`admin/nav.json`、`admin/registry.json`、`admin/controller.ts` | 素材一级导航；读取 `/api/admin/image-library` 和 facets | 搜索名称/标签、含已停用、重置；上传图片；查看缩略图/详情；改名称/描述/标签/分类/启停；删除；替换按钮只保留原页面意图，当前 API 没有模拟替换 |
| `mpLib` / 小程序素材库 | `web/src/admin/templates/mpLib.html`、同上 | 素材一级导航；读取 `/api/admin/miniprogram-library` | 标题搜索、包含停用历史、分页/重试；新建/编辑名称、AppID、页面路径、卡片标题、缩略图；启停、删除；“刷新缩略图缓存”只触发本地 cache resolver |
| `attach` / 附件素材库 | `web/src/admin/templates/attach.html`、同上 | 素材一级导航；读取 `/api/admin/attachment-library` | 搜索；上传 PDF；下载；编辑名称/标签；启停；删除。页面明确显示 PDF 不超过 10 MiB |
| 群邀请素材 | 无独立一级页面，也没有 `groupInvites` nav key | 通过生成 API `/api/admin/group-invite-library` 管理；在渠道欢迎语/群运营和固定内容包的素材选择器中按 ID 引用 | 本地卡片创建、编辑、归档；封面图片 ID 必须是本地 Media 图片；归档/停用前检查 Channel 引用；不得创建或调用 WeCom 入群能力 |

统一页面集成约束：后续由 v3-owned template/后端 adapter 将这些 donor 挂到 PR10 的 `internal/webshell/admin_base`。不得直接部署 donor 自带第二套 `.side` 页面，也不得为接壳改动 donor 文件。

## 请求 URL、方法和 DTO

以下是 donor 生成 API 的完整 Media 库请求面；生成文件仅作为原样前端供体保存，v3 `api/openapi.yaml` 未改动。

### 图片

| 方法 | URL | 请求/查询 DTO | 结果/行为 |
| --- | --- | --- | --- |
| `GET` | `/api/admin/image-library` | `GetLegacyImageListParams`：`limit`、`offset`、`enabled_only`、`q`、`category`、`tags`、重复 `tag_group`、`only_unlabeled` | `LegacyImageListSuccess`；默认启用行、严格分页和 facet 过滤 |
| `POST` | `/api/admin/image-library` | `LegacyImageCreateRequest`：规范 `data_url`、`file_name`，可选 name/description/tags/category/enabled | `LegacyImageCreateSuccess`；从规范 data URL 导入本地图片，不调用 Provider |
| `GET` | `/api/admin/image-library/facets` | 无 | `LegacyImageFacetsSuccess`；本地 category/tag facets |
| `GET` | `/api/admin/image-library/{image_id}` | `GetLegacyImageParams`：`include_data`、`variant` | `LegacyImageDetailSuccess`；禁用行可直接读取，variant 只返回相对地址/事实，不在读取时生成 |
| `PUT` | `/api/admin/image-library/{image_id}` | `LegacyImageMetadataUpdateRequest`：可选 name/description/tags/category/enabled | `LegacyImageMetadataUpdateSuccess`；TrimSpace、UTF-8、标签去重后更新 |
| `DELETE` | `/api/admin/image-library/{image_id}` | `DeleteLegacyImageParams.force`（兼容字段） | `LegacyImageDeleteSuccess`；force 不清理/绕过引用，存在引用返回 409 |
| `GET` | `/api/admin/image-library/{image_id}/variants/{variant_key}` | `variant_key`：`thumb_160`、`thumb_320`、`mobile_1080`、`large_1440`、`original` | 私有 blob/确定性 variant，支持 `ETag`/304；不调用外部图片服务 |
| `POST` | `/api/admin/image-library/upload` | `UploadLegacyImageBody` multipart：`image`、可选 name/description/tags/category | `LegacyImageUploadSuccess`；PNG/JPEG/GIF，<=10 MiB，按实际 bytes 解码并校验尺寸 |

### 私有 PDF 附件

| 方法 | URL | 请求/查询 DTO | 结果/行为 |
| --- | --- | --- | --- |
| `GET` | `/api/admin/attachment-library` | `ListLegacyAttachmentsParams`：`limit`、`offset`、`enabled_only`、`q` | `LegacyAttachmentListSuccess`；只返回 metadata，不返回 blob |
| `POST` | `/api/admin/attachment-library` | `LegacyAttachmentUploadRequest` multipart：`attachment`、可选 name/tags | `LegacyAttachmentItem`；规范 PDF，<=10 MiB；blob 与 metadata 同一 Media 事务 |
| `POST` | `/api/admin/attachment-library/upload` | 同上 | canonical create 的 legacy upload alias，语义相同 |
| `GET` | `/api/admin/attachment-library/{attachment_id}` | 无 | `LegacyAttachmentItem` metadata |
| `PUT` | `/api/admin/attachment-library/{attachment_id}` | `LegacyAttachmentUpdateRequest`：必填 `expected_version`、name/description/tags/enabled | CAS 更新 mutable metadata；file_name/mime/size/blob 不可变 |
| `DELETE` | `/api/admin/attachment-library/{attachment_id}` | 无 | `LegacyAttachmentDeleteSuccess`；Automation/Channel/Radar 引用存在时 409，读失败 fail closed |
| `GET` | `/api/admin/attachment-library/{attachment_id}/download` | 无 | 私有 PDF bytes；需认证，不在列表/详情泄露 blob |

供体还冻结了底层分片/内容包契约，作为 Media bottom layer 记录但不接线到中央层：

| 方法 | URL | DTO/结果 |
| --- | --- | --- |
| `POST` | `/api/admin/content-packages/preview` | `MediaContentPackageRequest`（name/content_text/enabled/refs）→ 本地预览 |
| `POST` | `/api/admin/content-packages` | `MediaContentPackageRequest` → 本地 content package |
| `PUT` | `/api/admin/content-packages/{package_id}` | 同上加 `expected_version` → CAS 更新 |
| `POST` | `/api/admin/attachment-library/uploads` | `MediaAttachmentUploadInitiateRequest`（file_name/name/description/size/sha256/enabled）→ upload ID |
| `PUT` | `/api/admin/attachment-library/uploads/{upload_id}/parts/{part_number}` | `MediaAttachmentUploadPartRequest`（sha256、base64 content）→ 204；part digest 必须匹配 |
| `POST` | `/api/admin/attachment-library/uploads/{upload_id}/complete` | upload ID → attachment ID；必须由 store 检查顺序、总大小、完整性 |

所有写请求的 HTTP 鉴权、CSRF、`Idempotency-Key`、错误映射和路由注册属于 Terra adapter；本 donor app 不持有事务外 Provider 调用。

## 关键 Journey / 状态边界

1. **图片上传/预览**：HTTP adapter 读取有限 body → `domain.Inspect` 校验文件名、声明 MIME、实际格式、完整解码、边长/像素和 10 MiB 上限 → `ImageUploadService` 在一个 UoW 中 reserve receipt、写 image+blob、追加 `media.image_created`、完成 receipt。读取列表/详情不创建 variant；variant 读取只接受固定 key 并返回私有 bytes/ETag。
2. **图片编辑/删除**：`ImageMetadataService` 仅更新名称、描述、标签、分类和 enabled，标签按 UTF-8/长度/去重规则归一化并产生 `media.image_metadata_updated`。删除先锁 Media 图片，再收集 Media-local、Automation、Channel、Radar 引用；任一引用读取错误即 unavailable，`force` 也不能绕过；有引用返回稳定引用列表，只有空引用才硬删除并追加 `media.image_deleted`。
3. **附件直传/分片**：直传先 sniff `%PDF-`、MIME 和上限，再把 metadata/blob/receipt/event 同事务提交；分片 initiate 绑定 sha256/size，part 只接受匹配 digest，complete 由 Media store 验证 part 顺序、总大小和最终 digest。下载始终是认证后的私有 PDF。更新使用 `expected_version` CAS；删除 fail closed 检查 Automation/Channel/Radar 引用。
4. **小程序卡片**：创建/更新先校验 AppID、pagepath、title 及本地 `thumb_image_id`。客户端提供 `thumb_media_id` 一律拒绝；只有 Media-owned `ThumbnailCacheResolver` 可写本地缓存 ID。`resolved`、`not_available`、`outcome_unknown` 都是本地完成事实，resolver 不得跟随 URL、调用 Provider 或盲目重试；停用/删除前检查 Channel 引用。
5. **群邀请卡片**：创建/编辑只保存本地 name/title/description/join_url/cover image/enabled；cover image 必须存在。停用/归档前检查 Channel 引用，归档只改变本地状态并追加 archive fact，不创建/删除/刷新 WeCom 入群链接。
6. **内容包**：preview/create/update 只检查被引用的本地 Media material 是否 eligible，bind 只保存本地绑定。该路径不产生发送任务、不拥有 Campaign/Outbound 表、不证明任何外部投递。

## v3 缺口对照及本次补齐

此前 v3 Media 只有 domain、read/list/detail/variant、content-delivery 底层契约，缺少可被 Terra adapter 实现的写侧应用边界。本次在 Media 内补齐：

- `internal/media/app/image_upload.go`：图片真实格式/尺寸校验、receipt replay、同事务 image/blob/event 契约。
- `internal/media/app/image_update.go`：图片 metadata/enabled 更新、规范化、版本时间单调性和 event 契约。
- `internal/media/app/image_delete.go`：引用聚合、fail-closed、force 不绕过引用、硬删除 receipt。
- `internal/media/app/attachment_library.go`：PDF 直传、列表/详情/私有下载、CAS 更新、引用保护删除。
- `internal/media/app/group_invite_library.go`：群邀请 list/get/create/update/archive、封面存在性和 Channel 引用保护。
- `internal/media/app/miniprogram_library.go`：小程序 list/get/create/update/delete、缩略图 cache resolver、Channel 引用保护。
- `internal/media/port/mutations.go`：Media-local event appender 与 Automation/Channel/Radar 的窄引用 port；没有跨领域 import。
- 对应 `*_test.go` 是 donor characterization tests 的 v3 module-path 适配版，覆盖 replay/conflict/rollback、边界校验、引用保护、缓存 outcome 和并发收敛。

仍需 Terra 处理：独立 v3 PostgreSQL 表/序列、blob 存储、receipt/audit/outbox 同事务 adapter；HTTP/OpenAPI/CSRF/idempotency header 映射；Composition Root 注册；part 顺序/过期/存储容量策略；PR10 webshell 的 donor 挂载。Terra 不应修改 donor 前端，也不应从 Media 直接 import Customer、OneID、Automation、Contact、Radar 的 store/app/http。

## 明确排除

订单、支付、退款、权益、会员、成员网格、客户/OneID、人群/Audience、Campaign、Outbound、历史导入、Provider 上传/发送/重试、v2 store/worker/runtime、`cmd/aicrm`、`api/openapi.yaml`、migrations、依赖锁、deploy/CI、`internal/platform`、`internal/access`、`internal/webshell` 中央路由均不属于本 commit。
