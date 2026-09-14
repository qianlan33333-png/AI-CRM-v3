# 客户目录标准选择组件按需加载 PRD

## 决策记录

**目标用户与结果。** 管理员进入客户目录列表或客户详情时，页面只装载标签选择器所需的标准组件；客户行、详情、同步和既有标签命令的行为不变。这样移除四个未使用 picker 脚本带来的网络与主线程竞争候选，不能据此声称已经修复后端 SQL 或首屏 SLA。

**边界分类。**

```text
OneID: not involved — 改动只调整浏览器资产的装载时机，不读取、解析、关联或写入客户身份。
Persistence: stateless — 不改变数据库、内部持久任务、标签命令、Provider 读写或外部效果；既有标签目录 GET 与客户列表/详情请求契约保持不变。
```

**前端基线。** 终端是管理端 `admin_base` 单壳；组件入口是 v3-owned `web/v3/standardComponentsHost.ts` 和客户目录 `internal/webshell/static/admin_console/admin_customers.js`。复用并扩展现有 `AICRMStandardComponents` Host，禁止建立客户私有 loader、壳、选择器或修改 `web/donors/*` 冻结 donor。

**GitHub 参考。** [Webpack LoadScriptRuntimeModule @ a943d69](https://github.com/webpack/webpack/blob/a943d69c4fd4e7b3edcdc03bce7c41eceef3bfd6/lib/runtime/LoadScriptRuntimeModule.js) 已核实存在。仅借鉴其 URL 级 in-flight single-flight、完成后清理与 `load/error` 分支原则；不复制 Webpack runtime，也不改变本仓的 assets URL 或 module system。

## 已确认的当前行为

`standardComponentsHost.ts` 的无参 `ready()` 将下列脚本串行加载：operation member、group chat、material、send-content composer、WeCom tag picker。文件末尾的 `void ready()` 又会在 Host 注入时无条件触发该全量序列。

客户目录在 `admin_base.html` 的 `.Customers` 分支加载该 Host；`admin_customers.js` 只消费 `AICRMWeComTagPicker`。它在 `startInitialLoads()` 中先启动 `loadTagSelectors()`，再并行启动客户列表/同步或详情请求。标签目录 GET 因此保留当前并行时机，不能被本 PR 延后、串行化或合并。

当前 `loadTagSelectors()` 捕获任何失败后只禁用 `<select>`，管理员无法判断原因，也没有就地安全恢复动作。

## 方案与合同

### 标准 Host

1. 将五个脚本保留在既有默认顺序中；公开的按需入口只接受 `readyFor(["tags"])`。其他四项能力不提供新的按需 API，仍只由无参 `ready()` 按既有五项顺序加载。
2. `readyFor(['tags'])` 只请求并完成标签 picker 的真实全局初始化后 resolve。未知能力必须返回可读错误。
3. 每个脚本由 source/能力级 single-flight 管理：多个并发 `readyFor(['tags'])` 共用一次请求和一次实际初始化；已有 `<script>` 若还 pending，后续调用必须等待其 load/error 结论，不能提前判 ready。
4. 任意预置同名 global（包括 `null`、空壳或旧 donor 实现）不是成功条件，也不能令 pending script 提前 ready。只有本 Host 等到 canonical source 的 `load` 事件后，验证该组件公开 API 已存在，才记录 capability ready；缺失时拒绝，清理失败状态和坏节点，允许下一次调用 retry。
5. 失败应清理 in-flight 与失败节点；retry 创建一项新请求。已成功的能力不得重复请求或重复初始化。
6. 标签 picker 的现有全局锁持续在标签能力真实成功后执行，防止冻结 bundle 覆盖其 global。
7. 默认 `ready()` 仍按既有 dependency order 加载五个能力；此前依赖全量 Host 的页面不改变。`readyFor(['tags'])` 后调用 `ready()` 只补齐剩余能力，反向顺序同样不得重复加载。
8. 关键遗漏修复：末尾自动启动不得再无条件 `void ready()`。只在真实客户目录挂载时自动执行 `void readyFor(['tags'])`。检测只能使用稳定、显式的客户页信号（优先 `document.querySelector('[data-customer-directory-root]')`，可由 `admin_base.html` 的 `.Customers` 装配保证）；禁止使用 URL、body class 或泛化选择器，以免误改变 frozen `message_history`、其他 customer 页面或全量 Host 调用者。非客户页面保持原来的全量 auto-load。

### 客户目录

1. `loadTagSelectors()` 改调 `AICRMStandardComponents.readyFor(['tags'])`，并只在它成功且标签 picker global 可用后启用并挂载控件。
2. 标签目录 GET 仍在 `startInitialLoads()` 的相同时机启动，且继续与列表/同步或详情加载并行。此 PR 不改变 GET URL、CSRF、标签预览/确认命令、权限、标签 API、客户 API 或客户分页。
3. Host 或标签目录失败时，保留禁用的原生 select，给每个受影响标签区域附近加入中文可读失败文本和显式“重试加载标签”入口。重试必须安全地重新执行本次 `loadTagSelectors()`，复用 Host retry，成功后移除错误并启用控件；不能重复创建 picker 按钮、重复注册效果或误提交标签命令。
4. 不改变客户标签全局锁、选择/取消/确认/清空、目录内容、客户标签结果回读和错误脱敏语义。

## 不做

- 不改客户目录、详情、同步、标签目录或标签命令的后端 API、权限、身份、持久化、Provider 调用和外部效果。
- 不延迟标签目录 GET 到首次点击；该项另立性能工作。
- 不重构 css、页面视觉、admin shell、冻结 donor、构建/CI 门禁。
- 不以此更改覆盖 PR #285（`5751c35c2bda908e45c81d3d9da82ccc0ede44dd`，客户分页乱序修复）；后续 delivery 分支须基于或整合该提交。

## 验收与验证

### Host 合同测试（扩展 `web/v3/standardComponentsRefresh.test.mjs`）

1. 客户 root 页面自动启动只请求 tags；没有 customer root 的非客户页继续调用默认 `ready()` 并保留五项原顺序。
2. `readyFor(['tags'])` 不请求其他四项；`ready()` 的兼容顺序与补齐行为正确。
3. 多个并发 tags 调用只插入一个 script；已存在 pending script 在初始化成功前不会 resolve。
4. `onload` 后标签 global 缺失会显示可读失败、清理节点与 promise，下一次调用可 retry；网络 error 走同一恢复语义。
5. 标签 global 成功后仍被锁定；全量默认调用回归。

### 客户页测试（扩展 `internal/webshell/static/admin_console/admin_customers.test.mjs` 及 `scripts/customer-directory-shell-e2e.mjs`）

1. 列表/详情只调用 `readyFor(['tags'])`，标签目录 GET 与客户列表/详情请求并行启动。
2. 成功时标签 picker 仍可选择、取消、确认与清空；已有标签命令 request/CSRF/结果回读断言保持。
3. 标签目录失败及 Host 脚本失败均有本地可读错误和重试入口；重试成功恢复控件且不重复按钮。
4. 现有客户列表、详情、分页、查询、清空、刷新、同步、手机号查看断言通过；PR #285 的分页并发回归在整合基线复跑。

### 必经质量链

- `node web/v3/standardComponentsRefresh.test.mjs`
- `node internal/webshell/static/admin_console/admin_customers.test.mjs`
- `node scripts/customer-directory-shell-e2e.mjs`
- `bash scripts/run-donor-view-consumers.sh check`（其中包含 `standardComponentsRefresh.test.mjs`）
- quality lane `frontend`，其中 `customer-directory-shell-e2e.mjs` 是直跑项。

浏览器复核只记录自然导航的同源 resource metadata：客户列表/详情不应请求四个无关 picker JS；仍需单独观察标签选择、失败重试与既有客户 API 读回。它是发布后验收门，不取代上述提交绑定测试，也不包含部署。

## 参考页面与审查记录

| 项目 | 结论 |
| --- | --- |
| 参考页面 | `admin_base.html` 的 `.Customers` 分支；`admin_customers.html` 的 `[data-customer-directory-root]` |
| 复用组件 | `AICRMStandardComponents`、`AICRMWeComTagPicker` |
| 公共扩展 | 在 v3 Host 加入能力级 `readyFor`，保留默认 full-load 合同 |
| 受影响调用 | 客户列表/详情显式 tags；其他既有无参 Host 调用继续 default ready |
| Product Design audit | 以现有页面/组件合同作窄加载路径审查；无重设计或新增视觉。生产客户页需要授权的已登录浏览器回读后才能形成截图级审查证据。 |
