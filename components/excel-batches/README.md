# Excel 群发批次组件

此目录为独立 Python 服务。上传、审核、发送采用同一个 V3 AI 助手计划；Python **没有发送、批准或绕过审核接口**。

## 边界与持久化

- Python：Excel 导入去重收据、不可变封面、批准时 A/B/C/D 快照、逐人观察和报表。单机单实例 SQLite WAL，独立持久卷；不访问 V3 业务表。
- V3：现有 PostgreSQL AI 审核记录；批准、快照引用、Outbound intent、External Effects/River 在同一个事务提交。发给企微的客户身份在 worker 执行时才通过 Identity Port 解析。
- 新增 0120/0121 迁移；原有非 Excel 私信行为保留。Excel 上传不预查员工或好友关系。原有平台代理能力开关和权限仍生效。
- 组件只监听 `127.0.0.1:8791`，使用至少 32 字符的 Bearer 密钥。浏览器只通过 V3 登录与 CSRF 保护后的接口访问，不能直接访问组件。
- 五分钟周期任务注册在现有 River，恢复后重新扫描原计划，逐页核对官方回执、更新已保存的观察；没有新增发送重试队列。只有成功回执的 `send_time` 能开始观察窗口。

## 部署准备（本次不部署）

1. 将本目录复制到同机独立目录，例如 `/opt/aicrm-excel`，创建虚拟环境并安装 `requirements.txt`。创建无登录权限的 `aicrm-excel` 服务账号。
2. 以 `config.example.json` 为模板填写真实 AppID、默认标题、PNG/JPEG 封面路径和只读数据源。数据库凭据文件仅服务账号可读。SQLite 文件、`-wal`、`-shm` 必须置于持久目录；备份使用 SQLite online backup API，不能只复制运行中的主文件。
3. 使用服务单元模板启动组件。`/etc/aicrm-excel/service.env` 配置 `EXCEL_BATCH_TOKEN`；V3 API 和 worker 使用相同的 `EXCEL_BATCH_TOKEN` 和 `EXCEL_BATCH_URL=http://127.0.0.1:8791`。默认二者均为空，即禁用组件。
4. 通过唯一 V3 发布路径应用 0120/0121 迁移、安装 Go 程序和完整 web/dist 产物；包含本目录的独立组件制品另行按同一发布版本交付。不要执行第二套 V3 构建上传路径。
5. `AICRM_SURVEY_OAUTH_OPEN_PLATFORM_ID` 需为 UnionID 所属开放平台的 ID；现有 AI 助手 dispatch / WeCom / External Effects 授权设置维持原有门禁。启用导入不等于启用发送。

## 数据源适配及上线前核对

示例 SQL 是按已观察到的 HXC 表/字段提供的适配模板，必须在目标只读数据库核对列名、内容路由、时间语义和权限后启用，不能将模板视为生产验证记录。

- `card_sql` 参数为完整 path，返回唯一 `title` 和 `cover_url` 或 `cover_base64`。URL 只允许 HTTPS 且域名在 `cover_hosts` 中，不跟随重定向；按实际可信 CDN 配置。查不到、模糊匹配或封面读取失败时使用完整默认卡片。
- `user_sql` 参数 UnionID，返回唯一 `user_id`；多个结果不猜测用户。
- `segment_sql` 参数 UnionID，返回唯一 `segment`，值为 A/B/C/D。必须复用原有分类投影/规则。当前检出缺少原规则源码，因此默认 null，并明确显示“未知”；不要临时自定活跃阈值。每次批准尝试有独立不可变快照，只有事务成功采用的快照会用于统计。
- `lesson_opens_sql` / `case_opens_sql` 参数为用户 ID、内容 ID、窗口开始、窗口结束，返回 `opened_at`。查询结果必须覆盖指定范围，不能附加全库固定条数上限。默认识别 `pages/article/article?lesson_id=...`、`pages/case/case?case_id=...`、`pages/case-detail/case-detail?case_id=...`；其他实际路由在适配器补充前显示“暂不可统计”。禁止以任意小程序访问替代内容打开。
- `coverage_sql` 参数为内容类型（lesson/case）、内容 ID、窗口开始、窗口结束，须从现有采集进度/日志覆盖记录返回唯一 `complete=1`，证明该内容和时间段的数据完整；采集暂停或范围不足返回 0。默认 null 时全部显示“暂不可统计”，即便事件表为空也不会算零打开。不能配置无条件 `SELECT 1` 代替覆盖证据。
- 返回的时间必须为 UTC；若源表保存北京时间，SQL 需显式转换，并相应转换查询边界。日志采集暂停、保留期不覆盖窗口或源表只有部分数据时，应使适配器返回 unavailable，不得把空结果宣称为无打开。
- 打开数据每次重新读取有界的 48 小时窗口，支持晚到补算。窗口数据不完整时整体打开率为 null，保留已观察到的打开人数，不悄悄缩小约定的分母。报表内保留每行窗口状态。

## 运维与异常

- 页面区分“未批准、待提交、任务已创建待员工执行、发送成功、失败、结果待核实”。企微创建群发任务不代表送达。
- 人员无法匹配、员工不正确、企微限制等异常保留在原行。超时未知结果不得换 key 重发；先用原 msgid 与 sender 查询证据。仅当完整分页且唯一匹配接收用户时采信成功。
- 批次和封面不可因服务重启丢失。组件不可用时保留原审核记录，暂停资料读取和观察；已提交的 V3 任务仍按现有 worker 行为执行。
- 同一 Excel 每位 UnionID 只允许一行，避免同一批次重复触达及打开率分母歧义。跨批次重发必须在上传页明确勾选创建新批次。
- SQLite 和 PostgreSQL 必须同时备份；停用仅取消组件配置，不删除审核、回执或观察历史。

## 验证

```sh
python -m unittest discover -s components/excel-batches -v
node scripts/excel-batches-dom-test.mjs
GOCACHE=/tmp/aicrm-go-cache python3 scripts/dev_preflight.py fast
GOCACHE=/tmp/aicrm-go-cache python3 scripts/dev_preflight.py compile
# 配置独立 AICRM_DATABASE_URL 后：
bash scripts/run-go-with-donor-views.sh go test ./cmd/aicrm -run 'TestPostgreSQLExcelImport|TestAIAssistant' -count=1
AICRM_REQUIRE_CHROMIUM_JOURNEY=1 bash scripts/run-go-with-donor-views.sh go test ./cmd/aicrm -run '^TestPostgreSQLExcelBatchesChromiumJourney$' -count=1
```

真实生产发送、实际分组数据和真实用户打开回传不属于本次本地测试证明的范围。
