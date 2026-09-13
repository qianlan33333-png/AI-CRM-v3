# PRD：后台权限治理与企业员工目录

## 业务判断

后台访问控制当前允许一个账号同时持有多个角色，也允许普通账号管理端点只按请求开始时的 Principal 角色判断。生产已确认存在两个 `super_admin`，不符合“唯一最终负责人”的交接和问责要求；现有企微联系人跟进人列表也只代表可跟进客户的子集，不能被当作企业中可授权员工的完整目录。

本 PR 将后台账号管理改为互斥的 `super_admin`、`admin`、`viewer` 三个角色，新增仅由企微**只读**通讯录提供的企业员工选择目录。它不会发送企微消息、创建外部效果、修改企微成员、创建客户或匹配 OneID。

- **OneID/外部身份：不涉及。** 企微员工 `userid` 是后台认证绑定值，不参与客户身份解析、归属或合并；大小写相异的既有绑定不自动归并。
- **持久化：涉及。** Access Owner 在单个 PostgreSQL UOW 内变更角色、登录开关、绑定、审计和会话版本。
- **外部效果：不涉及。** 企业目录只是 Provider 读取，在 Access UOW 外完成；不引入队列、重试状态机或 Provider 写。

## 参考与范围

- Apache Casbin 将认证主体与 RBAC 政策分开；其项目也明确不负责登录用户清单。本仓已有 Access Owner、PostgreSQL UOW 和 HTTP 授权边界，因此不引入 Casbin 或替换架构。[Apache Casbin](https://github.com/casbin/casbin)
- go-admin 的 RBAC 与按钮权限实践支持由服务端返回操作能力；本 PR 每一账号行返回受控 `actions`，前端不根据自己的角色推断能否写入。[go-admin](https://github.com/go-admin-team/go-admin/wiki/%E7%AE%80%E4%BB%8B)
- 企业微信“读取成员”接口只返回应用可见范围的成员；目录在 UI 中称“可授权企业员工”，不声称是全企业通讯录，也不复用客户跟进人列表。官方接口路径：[`/cgi-bin/user/get`](https://developer.work.weixin.qq.com/document/path/90196)。

本 PR 不改变客户、支付、退款、外部效果或企微 OAuth 配置。全项目的业务权限仍由后续统一矩阵收敛：admin/super 的业务能力必须一致，viewer 不得导出；本 Access 子 PR 只治理后台账号和目录。

## 两轴权限矩阵

| 后台身份 | 角色 | 可读账号/目录 | 可创建 | 可启停 | 可改角色 | 可改企微绑定/密码 | 可转移唯一负责人 |
| --- | --- | --- | --- | --- | --- | --- | --- |
| `KindAdmin` | `super_admin` | 是 | `admin`、`viewer` | `admin`、`viewer`，不得停用负责人 | `admin` 与 `viewer` 互转 | 非负责人账号 | 仅转给 active `admin` |
| `KindAdmin` | `admin` | 是 | 仅 `viewer` | 仅 `viewer` | 否 | 否 | 否 |
| `KindAdmin` | `viewer` | 否 | 否 | 否 | 否 | 否 | 否 |
| `KindStaff` / 其他 | 任意声明角色 | 否 | 否 | 否 | 否 | 否 | 否 |

普通创建和角色变更接口的 `role` 是单值，只接受 `admin` 或 `viewer`。不提供普通 `super_admin` 赋值路径。

## 唯一负责人、迁移与会话边界

1. `admin_user_roles` 迁移后每个账号只能有一个角色；`access_super_admin_control` 单例记录当前唯一负责人。
2. 空库允许 Bootstrap 在同一 UOW 创建第一个 active `super_admin` 和单例控制记录。
3. 已初始化数据库的迁移只在“恰有一个 active `super_admin`、所有账号均单角色、控制记录可验证”时通过。0 个、多个、停用负责人、角色异常或控制记录不一致都 fail closed，不猜测应保留哪个账号。
4. 生产现有两位负责人不写入通用迁移。最终发布窗口在 `0151` 之前先运行受审的一次性运维收敛命令：锁定 Access 行，按最高优先级标准化历史多角色，明确核验被批准的负责人，再原子更新角色、系统审计和受影响账号的 session version。此时控制表尚不存在，因此该命令不写它；`0151` 在已收敛状态上回填并验证单例控制记录。该命令不在本 PR 开发或 CI 中执行。
5. 每个写操作在 UOW 内重新锁定并核验 actor 是 active `KindAdmin`，其 session version 与授权时一致，且当前数据库角色仍允许该操作。被降级、停用或已 fence 的会话不能凭开始时的 Principal 继续写入。
6. 转移必须由当前负责人请求，并且 target 是 active `admin`。同一 UOW 将旧负责人降为 `admin`、目标升为 `super_admin`、更新单例控制记录、写审计并递增双方 session version。提交后旧负责人会话为 401；旧 Idempotency-Key 不能绕过重新认证或再次转移。并发请求最多产生一次状态转换。

## 企业员工目录与绑定

- 新 WeCom Owner Port 仅提供受限可见范围的成员 ID 分页和单成员最小投影（`userid`、显示名）；它不复用 `ListContactStaff`，也不能调用任何 Provider 写接口。
- `GET /api/admin/access/enterprise-employees?cursor=&limit=1..50&query=`：空 query 使用 Provider cursor 返回一页；非空 query 在服务端从可见全集扫描，以精确 `userid` 或显示名匹配。扫描未完成、令牌/权限/Provider 异常一律为 503，而不是错误地返回“没有员工”。查询结果游标与 query 绑定；Provider 是实时数据源，跨页变动只承诺实时 best-effort，不承诺快照事务。
- 所有 Provider 调用完成后才进入本地 UOW；不持有数据库锁等待网络。
- 候选项额外标明是否已有 Access 账号、其当前 role 和 login_enabled；目录中出现不等于获得后台权限。
- 创建账号或变更绑定时，服务端再次读取指定企业成员，要求返回的大小写完全一致，才可写入 Access。不会将 `huangyoucan` 与 `HuangYouCan` 合并，也不会用 follow-user、客户、OpenID 或任意历史绑定替代验证。
- Provider 失败、超时、权限不足不改变本地账号或绑定；日志只含受控错误类别和请求关联信息，不记录全局员工标识。

## API 合同

所有 API 要求现有后台会话；写操作另要求 CSRF 与 Idempotency-Key。响应 `Cache-Control: no-store`。

`GET /api/admin/access/users` 保留既有 `{ok, users}` 包络，并新增：

```json
{
  "ok": true,
  "actor": {"admin_user_id": 12, "role": "admin"},
  "capabilities": {"provision_admin": false, "provision_viewer": true, "transfer_super_admin": false},
  "users": [{
    "id": 14,
    "display_name": "…",
    "wecom_userid": "…",
    "role": "viewer",
    "login_enabled": true,
    "actions": {"set_login_enabled": true, "change_role": false, "bind_wecom_userid": false, "reset_password": false, "transfer_super_admin": false}
  }]
}
```

既有 `GET/PUT /api/admin/admin-access` 保持冻结客户端的读取封装；其 PUT 以及旧 `POST /disable`、`/roles`、`/wecom-userid`、`/password` 都委派到同一后端行级授权、幂等和会话重查，不能跨越上述矩阵。

新增或收窄的管理命令：

- `POST /api/admin/access/users`：`{wecom_userid, role:"admin"|"viewer"}`；服务端读企微成员后才创建本地账号。
- `PUT /api/admin/access/users/{id}/login-access`：`{login_enabled}`。
- `PUT /api/admin/access/users/{id}/role`：`{role:"admin"|"viewer"}`。
- `PUT /api/admin/access/users/{id}/wecom-userid` 与 `/password`：仅 super 的高级操作。
- `POST /api/admin/access/super-admin-transfer`：`{target_admin_user_id}`；只接受 active admin，旧会话提交后失效。

所有用户可观察的错误为受控中文/API code，不能透传 Provider 详情、密码、session、Token 或企业员工原始列表。

## 验收

真实 PostgreSQL 覆盖：空库 Bootstrap；迁移 fail-closed 组合；三角色矩阵；`KindStaff` 拒绝；写入前 role/version 重查；停用/角色改变后的 session fence；唯一负责人转移和并发；大小写不同绑定保留；目录分页、全域搜索、Provider 错误 503 与 UOW 外读取；绑定时二次精确验证；审计原子性。

HTTP/OpenAPI 覆盖：401/403/409/503、CSRF、Idempotency-Key、`no-store`、兼容 GET users 包络与冻结 admin-access 路由不绕过权限。前端仅按 `actions` 展示，不能将目录项误称已经授权。
