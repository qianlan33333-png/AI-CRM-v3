# PRD 03：通用机器授权、MCP 与旧外部业务 API

状态：批准开发，按名额派发；遵循总控。不以AI助手专用HMAC或凭据投影代替通用授权。

## 旧来源和V3差异

旧dd8d60d的 platform/platform_foundation/auth_platform/{api,models,service}.py、platform/admin_config/{api_clients,direct_api_key}.py、API Client/Direct Key管理模板；mcp_tool_catalog.py、channels/integration_gateway/{api,mcp,dispatch}.py、mcp_composition.py、router_registry.py。当前实际MCP三工具为resolve_customer/get_customer_context/get_recent_messages，旧文档未注册的写helper不算现成工具。

V3 Access已有session/RBAC和service principal类型，AdminOps direct_api_key/api_client仅安全状态投影和构造secret://引用，不是真实secret/hash/token；AI助手HMAC保持现有特定接入，不能冒充本模块完成。

## 用户流程和授权

管理员复用旧客户端/Direct Key页面：登记调用方、用途、owner数据范围、audience、scopes/capabilities、CIDR、到期、Token TTL；创建一次展示secret、轮换、启停/吊销、查看掩码/最近使用/审计。只存安全摘要，不再次回读secret。旧固定权限模板等价承接，不能API key→super_admin。

恢复 /oauth/token client_credentials，支持旧Basic/form凭据语义、请求scope只能收窄、audience严格验证、HTTPS/可信proxy/CIDR、no-store。短JWT默认旧30分钟、上限60分钟（已核实profiles.py默认1800、service.py允许60..3600秒），不引入refresh token或授权码OAuth产品。每次机器调用校验enabled/expiry/auth_version，轮换停用即时使旧secret/旧token失效；禁只验JWT签名直到过期。

Direct API Key保持旧只读权限模板，不可调用写接口。机器principal与后台用户分开，写命令身份/审计来自认证结果，不能从payload.operator提权。机器和后台各自鉴权后进同一领域Port。

## 实际旧接口清单（本轮全部登记承接）

以下列出/api/external前缀下7项GET和11项POST；完整机器接口另见03a-machine-route-inventory.md，亦属于本板块验收，不得因路径前缀不同遗漏。

| 方法/路径 | 旧文件 | V3 Owner与要求 |
|---|---|---|
| GET /api/external/orders | extensions/commerce/commerce/external_orders.py | Order只读、分页过滤/字段scope |
| GET /api/external/orders/{order_no} | 同上 | Order详情，不因猜order_no绕权限 |
| GET /api/external/users/resolve | 同上 | scoped Identity Resolve，不建客 |
| GET /api/external/radar-clicks | extensions/radar/radar_links/api.py | Radar受授权客户/范围查询 |
| GET /api/external/radar-links | 同上 | Radar只读 |
| GET /api/external/chat-records | extensions/archive/message_archive/api.py | Archive授权读取，不回旧库 |
| GET /api/external/questionnaire-submissions | extensions/forms/questionnaire/api.py | Survey只读、归属/分页 |
| POST /api/external/ai-audience/templates/preview | extensions/ai/ai_audience_ops/external_api.py | 既有人群定义预览 |
| POST /api/external/ai-audience/templates/apply | 同上 | 既有人群定义受控保存 |
| POST /api/external/ai-audience/spec/dry-run | 同上 | 既有声明式人群预览 |
| POST /api/external/ai-audience/spec/apply | 同上 | 既有定义保存；publish gate保持 |
| POST /api/external/ai-audience/spec/publish | 同上 | 既有定义发布与版本/权限 |
| POST /api/external/ai-audience/packages/{package_key}/archive | 同上 | 归档幂等 |
| POST /api/external/ai-audience/e2e/run | 同上 | 现有受控演练编排；旧main实际装配runner；恢复其受控业务编排与gate，不新建发送旁路 |
| POST /api/external/ai-audience/simple/preview | 同上 | 旧受限simple语义转现有声明式定义 |
| POST /api/external/ai-audience/simple/apply | 同上 | 同上保存；保留retired webhook配置410 |
| POST /api/external/ai-audience/simple/{package_key}/activate | 同上 | 既有人群激活/刷新，River和业务gate |
| POST /api/external/ai-audience/simple/{package_key}/archive | 同上 | 既有归档 |

首次实施冻结每路由请求/响应/错误/权限/数据范围和旧实际注册证据；表中7+11计数正确性以fixture路由清单为准；还须覆盖03a登记的其他真实机器路由。旧业务prefix gate、publish gate、废弃参数拒绝不能省。受限SQL输入只接受可证明等价翻译的旧允许子集，转换现有声明式规则；绝不将外部SQL直接执行，也不为兼容创建新数据库查询平台。若现有领域缺必要语义，提交具体缺口由根协调领域Port并纳入本板块同一个完整PR；仅已存在且可独立定位的共用基础缺陷另走小PR。禁止用501/伪200充作全项完成。

按用户最新指令，机器鉴权、三MCP工具、全部GET/POST业务适配、管理前端、历史导入及运行测试必须在一个完整板块PR内收口；可拆实施提交，不拆业务PR。全部清单有实际等价结果或有证据的旧本来不可用边界才完成，不能擅自缩成只读平台。

## MCP契约

恢复GET/POST /mcp及initialize/tools/list/tools/call；以旧JSON-RPC和protocolVersion 2024-11-05为兼容基线。工具按权限发现，输入schema/错误与旧fixture一致；未知工具/方法、bad JSON、非法ID/批处理范围清晰拒绝。三个工具调用Customer/Identity/Archive既有Port，不跨表。

resolve_customer兼容customer_ref/scoped external_userid等旧实际支持输入；对mobile仅允许通过既有可信Identity语义解析，缺scope/冲突保持pending，不隐式建客。context/recent_messages只返回授权且V3已有记录；存档未就绪如实状态，不绕gate或远程旧环境读取。不新增MCP写工具。

## Owner、事务与历史

OneID：客户API/工具读取canonical、scoped解析，不provision；凭据自身不涉及。持久化：Access本地UoW，已有业务写通过原领域UoW/River/EER；不新建机器执行框架。

Access独占真实机器clients、credential digest、auth_version、grant与audit；AdminOps安全管理投影通过稳定Port。token签名安全引用来自现有受保护配置，禁止任意ref或明文配置。Host层仅认证/DTO适配/Port调用；读范围必须进入领域查询，禁止先取全量再无上限过滤。PII字段授权和日志脱敏。

Client管理同事务状态/版本/审计/幂等；轮换安全返回一次凭据，重放不能凭明文补存。业务写稳定幂等键/receipt保留现有审批与Provider gate；外部调用不能跳过审批自动发送。

历史调用方/授权/审计离线映射，旧hash算法/secret不可验证的客户端默认disabled/reissue_required；现有AdminOps投影绝不能导入即成为有效凭据。历史Token不重签恢复。源ID幂等/每行结果，secret不出文件日志。迁移0096。

## 验收

- A01 管理页面创建→一次显示→token→真实授权API/MCP→轮换/停用→旧secret/token立即拒绝；到期/CIDR/audience/scope和可信proxy覆盖。
- A02 三真实MCP工具schema/JSON-RPC/错误/授权/identity冲突/存档边界；无隐式建客与跨scope。
- A03 7GET路径fixture和实际Port结果等价，分页、PII和owner范围不可绕过。
- A04 11POST逐路由旧行为对照，preview/apply/publish/activate/archive真实领域事实；无rawSQL执行/审批绕过/新Provider旁路。旧本来disabled能力单列证据。
- A05 真实PG并发client轮换/停用/幂等/回滚与重启；业务API重放不重复效果。
- A06 UI/API docs真实凭据状态与工具发现；历史导入不启用旧token、不泄密且可对账。

测试Provider和真实业务验收分开。仅有认证、metadata、空工具或排队不能标本板块完成。

## 补充核查（首次派发前冻结）

旧main.py:120确实注入ai_audience_e2e_runner_factory，不能援引fallback503声称旧能力不存在。其默认gate关闭、指定测试对象、显式确认与最大真实发送次数约束必须保留；本轮仅在隔离测试Provider验证，不使用旧硬编码真实对象进行发送。运营周期/AI计划/群广播/完整人群包机器接口按03a清单逐项承接现有业务，不扩展新产品。

## 冻结供体复用清单（本次确认收口）

旧仓 https://github.com/qianlan33333-png/AI-CRM，提交 `dd8d60dd8ddb983aca2ec88cc9e65a9f7563f79f`。下表与总控最新规则共同生效；已有V3实现优先复用，实际完成状态以验收矩阵当前HEAD为准。

| 分类 | 冻结依据/复用对象 | 收口要求 |
|---|---|---|
| 原样复用 | integration_gateway/mcp.py、mcp_tool_catalog.py 的协议/工具样例；admin_config/api_clients.py、Direct API Key相关页面的字段与顺序 | 复用实际旧契约，前端经Host/Adapter接入 |
| Go 等价迁移 | 调用方/Key/Token生命周期、授权、MCP发现/调用和03a冻结56条method/path | 同一个完整板块PR；不能以只读工具或少数API代替全部约定 |
| V3 已有 | Access会话/RBAC、客户/会话/AI等业务稳定Port、现有Go路由和OneID | 机器主体不伪装超级管理员，业务写复用既有事务和执行链 |
| 待补齐 | 通用机器凭据实际校验、运行装配、旧页面适配、历史停用导入和完整协议/PG/浏览器证据 | 不新增任意SQL查询平台，不迁旧运行依赖；尚未有完整PR |

## 实际旧授权模板核对补充

旧 `platform/platform_foundation/auth_platform/profiles.py:105-112`、`platform/admin_config/api_clients.py:46-54` 和 `scripts/ci/update_route_policy_manifest.py:385` 的 MCP 使用 `audience=external_integration`，`scopes=read/write`，`capabilities=mcp_read/mcp_execute`，purpose=mcp。保持旧调用方请求：GET/read和POST/write按对应capability/purpose校验；不得强迫改用新增audience=mcp或scope=mcp。token请求scope只能收窄，不能凭客户端存在write capability绕过token的read范围。

旧 `api_clients.py:123-232` 的创建默认停用、轮换后停用、停用后可编辑display_name/Token TTL/CIDR均需承接。管理页面原client_type/token_ttl_minutes等字段通过HTTP DTO/Host薄映射到Go稳定Port；底层安全摘要/认证版本保留，勿为了原样复用而恢复明文密钥。

## 历史与机器主体边界补充（具体供体审核）

- source_revision记录冻结源码版本；快照批次独立命名。相同源码版本允许后续抓取，来源作用域与源行身份跨批次幂等；相同行重放、重叠快照新增行、同源行漂移分别验证。不能以批次ID掩盖重复授权或审计事实。
- 旧owner_scope中的customer_id等本地数字引用只作为源事实，经既有可信映射转换后才能参与V3授权；原值与映射回执保留。未映射或冲突保持不可启用，禁止轮换密钥解除此阻断，也禁止删除限制变成全量授权。外部owner_userid须验证同企业作用域。
- 旧auth_platform/profiles.py的group_broadcast是principal_type=service且audience=external_integration，属于本轮已冻结服务模板；不能因不是api_client全部排除。internal_worker模板仍不对外恢复。
- 旧Direct API Key记录为client_id=aicrm-direct-external-api-key、purpose=external_agent、read/external_read；若V3使用direct_external_api_key/direct_api_key，迁移须显式登记映射并供原Direct页面回读，不能误入普通OAuth调用方。保持停用待重颁、只读和来源审计，不迁旧secret/token。
- 机器主体进入业务命令优先使用现有string actor或ActorService。需要兼容只支持人工int64 actor的领域时，使用领域Owner最小actor_kind/actor_ref适配并保留人工字段兼容；不得把机器client数值当管理员ID。实际机器client须可审计追溯，不另建身份或授权平台。
