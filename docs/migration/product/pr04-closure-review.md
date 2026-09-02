# PR04 商品管理 donor 闭包复核（只读）

本复核以用户批准的 PR04 商品管理迁移计划为准，复核固定 donor AI-CRM-v2
`6bfbe5816bb89913c70adaca87d6a486260e016e`、v3 已合并基线
`015587164bd52ee5d24978aa07970b7c782fbb1e`，并核对 prep `b0bd173` 与前端
audit `ffc2bab`。复核在独立 `codex/pr04-closure-review` worktree 完成；没有修改 donor
前端字节、Composition Root、OpenAPI、migration、deploy、lock 或共享端口。

## 结论

**NOT CLOSED（不能作为 PR04 完成验收）。**

冻结前端的 24 个 PR04 文件与 donor 逐字节一致，donor 的实际构建和 TypeScript 检查也能通过；
但 v3 基线目前只有两个商品入口的 generic placeholder，没有 Product HTTP/store/UoW/migration/root
journey，也没有将 donor body 接入 PR10。直接提供 donor 静态页面还会带入第二套 `.shell/.side`。
另外，24 个冻结文件不是浏览器 runtime 的完整依赖闭包：固定的 `main → legacy → AdminController`
会在实际商品表单上发出若干共享读取请求。活动页面已经发出的请求不能靠前端改写、裁剪或“抑制”来
避开；必须由现有稳定 Port 或受控兼容 adapter 返回 donor mapper 接受的 exact DTO，或者返回
truthful、页面仍可用的 fail-closed 投影。只有未进入的排除页面/分支才可以在 bundle/template/API
之前拒绝。不能以“文件已复制”“构建成功”或 HTTP 200 代替闭环。

硬门禁如下：

- 浏览器运行链保持 donor 实际链路 `web/scripts/build.mjs → web/src/admin/main.ts →
  web/src/admin/legacy.ts → AdminController`；禁止另写 product-only loader，禁止改写
  `main.ts`、`legacy.ts`、`AdminController` 或业务模板来接壳。
- 只允许从 unchanged donor bundle 的 `template#tpl` 取 allowlisted 商品 body，挂到 PR10
  `internal/webshell/templates/admin_base.html` 的唯一壳中。donor 的完整 HTML、`.shell`、
  `.side`、旧 nav 和第二份 document 不得输出。
- 只允许 `products`、`productForm`、`spProducts`、`spProductForm` 四个业务页面；
  `spProductData` 是明确 denylist，不得加载模板、页面分支或 member-grid 请求。
- 商品与周期商品的 HTML/TS/CSS/assets/API DTO、文字、默认值、交互顺序、URL 生成行为均按
  donor 保持；v3 只提供 route/auth/adapter/backend compatibility，不做前端补功能、美化或
  另一套字段实现。

## OneID、持久化和外部效果分类

- **OneID/外部身份：不涉及。** 商品的业务身份只有 Product 自身 ID；PR04 不解析客户、手机号、
  `openid`、`unionid`、`external_userid`，不绑定客户，不做自动建客或合并。
- **持久化：涉及。** Product 定义、生命周期、版本、admin projection、幂等收据、审计事实和
  本地事件必须由 Product-owned Store/UoW 在同一 PostgreSQL 事务中原子提交；更新、启停、复制、
  归档/删除需要 CAS。
- **外部效果：仅本地配置/测试事实。** external-push 只保存
  `product_kind`、`id`、`enabled` 和 opaque `configuration_reference`，本轮不得调用企微、
  微信、支付 Provider。Provider 写入、签名、回调重放、`accepted/queued/attempted/executed/`
  `outcome_unknown/reconciled`、重试与对账均属于 outbound/External Effects；未知结果不能换
  幂等键盲重试。

## 冻结前端与实际构建证据

### 文件数、哈希和构建

PR04 的冻结清单是 **24 个** `web/src/**` 文件（controller/main、registry/nav、四个 allowlisted
商品模板、二维码/工具、商品和周期商品生成客户端、transport/shared API/UI 等）。没有独立商品
CSS、图片、字体二进制；模板使用内联样式，二维码依赖 `qrcode-generator@1.5.2`，图片 URL 是
运行时 Media 数据。

证据结果：

| 门禁 | 结果 |
| --- | --- |
| donor commit | `6bfbe5816bb89913c70adaca87d6a486260e016e` |
| donor/staged SHA-256 | **24/24 一致** |
| donor/staged `cmp` | **24/24 通过** |
| donor `npm run build` | **通过：56 pages + 9 hashed entries** |
| donor `npm run typecheck` | **通过** |
| donor `npm run version:check` | 当前复核环境失败：Node `v25.9.0`，项目要求精确 Node `v24.18.0`/npm `11.12.1`；不能沿用旧 audit 的“已通过”表述 |

已运行的冻结校验命令（完整 donor clone）：

```sh
PR04_DONOR_ROOT=/private/tmp/aicrm-v2-pr04-donor-clone.rFhMoY \
  /private/tmp/aicrm-v3-products-audit/scripts/check-pr04-donor-manifest.sh
# PR04 donor frontend freeze PASS: donor=6bfbe5816bb89913c70adaca87d6a486260e016e files=24
```

v3 基线 `web/src` 与固定 donor 的 24 个路径逐一 `cmp` 也是 24/24 通过。这个数字是 PR04
allowlist 的冻结文件数，**不是完整浏览器 runtime 的传递闭包数**：`legacy.ts`、其动态 history
 chunks、混合 `api/admin.ts` 和其他 PR01 donor snapshot 依赖仍然很宽。这是构建依赖事实，不是
前端可以被改写或按页面裁剪的权限：页面/document 边界仍需由 route adapter allowlist，API 仍需由
服务端认证/能力边界保护；但凡 allowlisted page 的 unchanged runtime 实际发出的读取请求，都必须
走对应 owner 的稳定 Port/兼容投影并返回 exact DTO。仅对没有进入 allowlisted page 的路由/分支（尤其
`spProductData`）做前置拒绝，不能把宽 bundle 当成商品能力已闭合。

### donor 实际入口和第二壳事实

`web/scripts/build.mjs` 从 `web/src/admin/registry.json` 读取页面，从
`web/src/admin/main.ts` 生成 admin entry；普通页面由 `adminPage()` 输出 `<template id="tpl">`
和 admin bundle。`adminShell()` 同时输出完整 v2 document、`<div class="shell">`、
`<aside class="side">`、旧 nav 和 `main#stage`。商品 registry key 包括 `products`、
`productForm`、`spProducts`、`spProductForm`、`spProductData`，所以 donor 构建会产生五个对应
静态页面（后者仍是本轮排除项）。

运行时 `main.ts` 只对 customers 做特殊分支，其余页面动态导入 `legacy.ts`；`legacy.ts` 取得
`template#tpl`，创建 `new AdminController(api, page)`，执行 `controller.init()`，然后把
模板挂到 `stage`。这条宽 donor 链必须原样保留，不能为了商品新造 loader 或改变前端 bundle。

v3 基线目前 `/admin/wechat-pay/products` 与 `/admin/service-period-products` 仍由
`internal/webshell` 输出 placeholder；该 placeholder 只有一个 PR10
`<aside class="admin-sidebar">`，但没有商品能力。因而当前基线**没有已经运行的第二壳，
却也没有商品闭环**；如果直接把 donor `dist/admin/*.html` 作为响应，就会确定性地出现 donor
`.shell/.side` 与 PR10 `.admin-sidebar` 的第二壳。验收必须同时满足：响应中恰好一个 PR10
sidebar、无 `.side`、无 donor `.shell`/旧 document，并且页面 body 来自 unchanged donor
`template#tpl`。

## 五个页面 key：canonical、nested alias 和 denylist

以下是 adapter 必须锁定的 URL 合同。canonical 是 v3-owned 路由族；alias 是从 donor 未改动的
`goto(page) = page + '.html' + query` 及不同当前目录解析出的同源 nested/静态结果。alias 也必须
经过同一 Session、管理员权限、CSRF（写操作）和审计门禁；不能因是 `.html` 而退回 donor 壳。

| key | v3 canonical | donor 实际 alias（原样链接解析） | 处理 |
| --- | --- | --- | --- |
| `products` | `/admin/wechat-pay/products` | `/admin/wechat-pay/products.html`（从 ordinary 当前目录）；`/admin/products.html`（从 service-period 当前目录/直接 donor build） | allow；body 只取商品列表 template |
| `productForm` | create `/admin/wechat-pay/products/new`；edit `/admin/wechat-pay/products/{id}/edit` | `/admin/wechat-pay/productForm.html`、`/admin/wechat-pay/productForm.html?id=<id>`；从 root donor 目录来的 `/admin/productForm.html`、`/admin/productForm.html?id=<id>` | allow；adapter 将 alias 的 `?id` 精确转成 canonical form context，不能改 donor `goto` |
| `spProducts` | `/admin/service-period-products` | `/admin/spProducts.html`（service-period 当前目录/直接 donor build）；`/admin/wechat-pay/spProducts.html`（ordinary 当前目录） | allow；body 只取周期商品列表 template |
| `spProductForm` | create `/admin/service-period-products/new`；edit `/admin/service-period-products/{id}/edit` | `/admin/spProductForm.html`、`/admin/spProductForm.html?id=<id>`；ordinary 当前目录的 `/admin/wechat-pay/spProductForm.html` | allow；同上，保留 donor query/交互语义 |
| `spProductData` | **无 canonical** | `/admin/spProductData.html`、`/admin/wechat-pay/spProductData.html`；若带尾斜杠 alias 被接受，也包括 `/admin/service-period-products/spProductData.html` 等同目录解析结果 | **deny before bundle/template/API**；返回受控 404/403，不能渲染 member-grid、历史、客户/权益数据 |

`/admin/wechat-pay/...` 与 `/admin/...` 的 nested 差异不是可忽略的“旧链接”：例如从
`/admin/wechat-pay/products` 解析 `productForm.html?id=7` 会得到
`/admin/wechat-pay/productForm.html?id=7`，从 `/admin/service-period-products` 解析同一类
链接会得到 `/admin/productForm.html?id=7`。adapter 必须覆盖实际出现的同目录 alias 和
query，不得只注册两个 list canonical。所有 `spProductData` 变体必须在选择 donor page、执行
legacy init 或发起 API 之前拒绝；只隐藏列表按钮不构成边界。

## 页面、交互和依赖闭包

### allowlisted 页面

- `products`：商品编码/名称/价格/状态/已售卖/更新时间和编辑、复制、启停、删除。普通商品
  分享保持 donor 的 `no_authoritative_public_purchase_route` blocked 语义，不能伪造购买链接。
- `productForm`：`#product-sale`、`#product-media`、`#product-action`、`#product-wecom`、
  `#product-push` 五段和重复保存按钮；保留 donor 字段、默认值、价格转 minor、非负库存、
  页面素材、引流渠道/小程序、完成跳转 JSON、opaque `wecom_tagging`、本地 external-push。
- `spProducts`：周期商品列表、编辑、复制、启停、归档和 local-only 分享；`数据` 操作必须
  blocked/不可用，不能指向 `spProductData`。
- `spProductForm`：`#sp-sale`、`#sp-media`、`#sp-action`、`#sp-push` 四段和原始字段/默认值，
  只保存商品 projection 与本地 external-push。

分享必须保留 donor 行为：普通商品没有权威购买路由时阻断；周期商品只接受
`/p/service_period/<positive-id>` 且 `local_only=true`，不代表支付、会员发放或 Provider 执行。
二维码由冻结 `qr.ts`/`util.ts` 提供，不能重绘或改下载行为。

### 实际 API/DTO 读取与禁止混入

`web/src/api/admin.ts` 是混合总控制器，不是可以整体注册的 Product handler。对固定 donor build
以真实 `main → legacy → AdminController` 启动并用浏览器 CDP 采集到的实际请求图如下；这些是
allowlisted 页面已经发出的网络请求，不能靠修改、裁剪或“抑制”前端来消失：

- `products`：只发出 ordinary Product 列表 GET。
- `productForm` 新建：实际读取 channels、ordinary products、image-library、tag-groups 和 tags。
  `productForm?id=1` 还实际读取 Product 详情、`local-entitlements` 和 ordinary external-push GET。
- `spProducts`：只发出 ServicePeriodProduct 列表 GET。
- `spProductForm` 新建：实际读取 channels、service products、image-library、tag-groups 和 tags。
  `spProductForm?id=1` 还实际读取 service detail、members、member-grid access/schema/views/
  share-settings 和 service external-push GET。
- `spProductData?id=1` 才实际发出 member-grid query；该整个页面仍在前置 denylist，不得挂载。

因此 route/API gate 必须区分“活动页面真实请求”和“仅存在于 bundle 的未进入分支”。Product、
Media、Channel、Tag 和本地 external-push 的活动读取分别通过各 owner 的稳定 Port/兼容 Adapter
返回 donor mapper 接受的 exact DTO；不得跨域读表。`local-entitlements`、members 和 member-grid
读取虽然属于本轮排除域，却是冻结编辑页确实发出的请求：若既有 owner 能提供不含客户身份、
不含成员明细的 truthful unavailable/empty-capability projection，且 donor 页面能继续工作，可由
后端兼容 Adapter 返回；否则该编辑页和 PR04 必须保持未闭环。禁止返回 Seed/Mock、伪造“可用”、
读取排除表，或把 HTTP 200 本身当成能力完成。

Tag 目录结果即使当前模板只保存原始 `wecom_tagging` JSON，也不能否认浏览器已发出的请求；PR04
只能消费 PR03 的稳定 Tag read Port。`spProductData`、member-grid query/history、客户/权益写入和
其他未进入的 bundle 分支继续在模板或 API 前 fail closed。

- 生成 external-push client 虽包含 POST test wrapper，但当前商品模板/`api/admin.ts` 只使用
  GET/PUT 配置；POST test 是后端本地 acceptance seam，不得被描述成活跃页面交互，也不得因
  bundle 存在而开放其他未使用页面/API。
- `productPageDto`/`serviceProductPageDto` 当前用 `Number()` 直接转换 ID/version，
  `priceMinor` 也没有安全整数上限。v3 adapter 必须校验正的 safe integer ID/version、非负
  int64 `price_minor` 和响应对象归属，不能把宽松转换当作 DTO 闭包。

冻结 donor 的一个已知行为差异也必须保留并在验收中记录：素材 picker 的 UI 选择上限是 10，
但保存 projection 接受最多 20 条 URL（每条最多 2048、同源路径或 HTTPS）。不能改 donor 字节；
backend DTO 可按已冻结合同接受 1..20，但 journey 必须分别验证 UI 的 10 条选择行为与后端的
20 条上限，避免把两者误报为同一限制。

## 后端 donor 能力与闭包缺口

### prep 已证明的能力

`b0bd173` 的 v3 Product app/port 测试证明了以下本地能力边界：

- ordinary Product 与 service-period projection 共用 Product-owned identity；ordinary lifecycle
  为 `draft|enabled|disabled`，service lifecycle 为 `draft|enabled|disabled|archived`。
- **两种商品列表实际都是 1..100。** ordinary 使用 opaque cursor；service 使用 offset，offset
  为 `0..1,000,000`。任何文档/adapter/API DTO 继续使用更大的 ordinary 上限都应视为错误。
- create/update 会校验 code/name/description、非负价格和库存、币种、images、admin projection；
  update/enable/disable/copy/archive/delete 使用 expected version + CAS。
- ordinary delete 只允许未引用 draft；service delete 是保留历史事实的 archive；copy 不复制
  Provider、order、entitlement、membership 或 customer effects。
- external-push 只保存本地配置，test acceptance 的
  `provider_accepted/delivery_proven/real_external_call_executed/auto_retry_allowed` 均必须为
  false；Product 不应直接持有 Provider/Worker/队列。
- Product 写入、幂等 receipt、audit fact、事件/Outbox 必须在同一 PostgreSQL UoW 原子提交；
  并发读写不能用两个独立 HTTP 请求“模拟原子”。

### donor 后端可借鉴但不能直接开放的能力

固定 v2 donor 还包含 local mutation、service-period member-grid/history、settlement、
entitlement、customer 关联和 external-push handler。PR04 只能取商品定义/lifecycle 和本地
external-push 配置的叶子协议；不能把 donor 的 member-grid、history、entitlement、订单、支付、
客户或 OneID 表/handler/store 接进 v3。

### 当前 v3 缺口（阻断闭包）

在 `0155871` 基线中，`internal/product` 不存在；`cmd/aicrm/composition.go` 未注册 Product
module/HTTP handler，商品 canonical route 仍是 webshell placeholder。因此 prep/audit 仍是准备
和审计材料，不是可运行迁移。尚缺：

1. Product-owned PostgreSQL Store、独立 v3 migration、HTTP route/DTO、管理员 auth/CSRF、
   UoW/CAS/receipt/audit/outbox 和 composition wiring；本复核不实施这些变更。
2. prep 的 `internal/product/port/events.go` 只是 local EventAppender seam，尚未接 v3 版本化
   event/outbox；应由后续适配 lane 负责，不能直接 dispatch Provider。
3. external-push save 在 donor 前端是“先保存 Product、再用另一随机 Idempotency-Key 保存
   external config”的两个请求；不能通过改前端把它伪装成一个原子请求。每个请求内部仍须把
   自身状态、receipt、audit/event 同一 UoW 提交，并按原请求 key 重放；第二步失败时必须返回
   truthful partial state 供重试/对账，不能回滚已经提交的第一步、静默宣称整页保存成功或另造 key
   盲重试。
4. `CommerceExternalPushService.SaveExternalPushConfiguration` 的 prep seam 预留 receipt，
   但没有明确 Product domain audit event；后续必须补齐“谁、何时、哪一对象、旧/新配置、结果”的
   不可变审计事实并与状态/receipt 同事务。
5. Media 只能经现有 `internal/media/port` 的 image list/metadata/reference 能力；Product
   不能读 Media 表。Channel 目录需要稳定 read Port；Tag 目录消费 PR03 稳定 read Port。冻结
   编辑页实际发出的 local-entitlements/members/member-grid 元数据请求必须按上节提供 truthful
   兼容投影或把页面标成未闭环，不能跨域直读表，也不能修改前端去掉请求。
6. 前端 route adapter 必须先做 canonical/nested alias 归一和 `spProductData` deny，再选择
   page/template/legacy 分支；否则宽 bundle 会暴露未使用 API 或第二壳。

## prep/audit 错误与遗漏复核

已确认的准备材料问题如下：

- prep manifest 的 ordinary list 上限记录错误；实现和本复核合同统一为 **1..100**，service
  也是 **1..100**（offset `0..1,000,000`）。后续不应恢复旧上限。
- audit 声称 Node `version:check` 已通过，但当前可复核环境是 Node 25；该结果依赖 Node 24.18
  环境，不能作为本次独立证据，必须重新在精确工具链验证。
- audit 只写了 `.html → canonical` 的概念映射，没有列出 ordinary 当前目录和
  service-period 当前目录导致的 nested aliases；也没有把 `spProductData` 的所有同目录变体
  作为“bundle/template/API 前置 deny”。
- audit 把 24 个冻结文件近似当作完整 runtime；实际 bundle 仍经 `main → legacy →
  AdminController` 导入混合域，必须同时纳入 PR01 transitive closure 和 server-side route/API
  allowlist。不能为规避宽 bundle 编写 product-only loader。
- audit 没有用真实 browser request graph 区分活动请求和 dormant bundle 分支，并曾暗示可以
  抑制 Tag/local-entitlements/member-grid 元数据读取；实测证明这些请求会在相应 form 上发生，
  必须由稳定 Port/兼容投影处理，不能改前端或跨域读表。
- audit 将生成的 external-push POST test wrapper 与活跃商品页面操作并列；当前 UI 未导入/调用
  该 wrapper，必须把它标为后端本地 acceptance 能力，不得开放为未使用 UI/API。
- audit 未突出素材 picker UI 10 条、保存 DTO 20 条的 donor 差异，也未阻断
  `Number()` ID/version 转换、price safe-int 和 Product/external config 的双请求随机幂等。

## 验证与验收门禁

本次已运行：

```sh
# v3 prep 商品 app/port
go test -count=1 ./internal/product/...
go vet ./internal/product/...
go test -race -count=1 ./internal/product/...

# v3 基线相关壳/Media/Channel
go test -count=1 ./internal/webshell ./internal/media ./internal/channel/...

# 固定 donor 商品全包
go test -count=1 ./internal/product/...
go vet ./internal/product/...

# donor frontend
npm run build
npm run typecheck
```

上述 Go 测试全部通过；donor build/typecheck 通过；精确 Node 版本检查因环境不符失败，不能
忽略。最终 Product PR 在实现 lane 必须另外通过 fresh PostgreSQL 16 与 upgrade migration、
管理员登录/权限/CSRF、两种商品创建→编辑→刷新持久化→审计、CAS 并发、稳定幂等重放、Media/Channel
adapter、local external-push acceptance，以及 canonical/nested alias journey。

验收页面响应必须满足：一个且仅一个 PR10 `admin-sidebar`；零 donor `.side`、`.shell` 和旧
document；allowlist 外页面/API（尤其 `spProductData`、member-grid、entitlement、customer/OneID）
在模板和网络请求前拒绝；冻结的 24 个文件和 PR01 transitive runtime 均能从固定 donor hash
重建。真实 Provider 必须保持 disabled，不能用 queued/accepted/HTTP 202 冒充已执行或已对账。

本分支只记录复核，不改变上述实现边界。
