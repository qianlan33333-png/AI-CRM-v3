# PRD 03：通用机器授权、MCP 与旧外部业务 API

状态：批准开发，按名额派发；遵循总控。不以AI助手专用HMAC或凭据投影代替通用授权。

## 旧来源和V3差异

旧dd8d60d的 platform/platform_foundation/auth_platform/{api,models,service}.py、platform/admin_config/{api_clients,direct_api_key}.py、API Client/Direct Key管理模板；mcp_tool_catalog.py、channels/integration_gateway/{api,mcp,dispatch}.py、mcp_composition.py、router_registry.py。当前实际MCP三工具为resolve_customer/get_customer_context/get_recent_messages，旧文档未注册的写helper不算现成工具。

V3 Access已有session/RBAC和service principal类型，AdminOps direct_api_key/api_client仅安全状态投影和构造secret://引用，不是真实secret/hash/token；AI助手HMAC保持现有特定接入，不能冒充本模块完成。

## 用户流程和授权

管理员复用旧客户端/Direct Key页面：登记调用方、用途、owner数据范围、audience、scopes/capabilities、CIDR、到期、Token TTL；创建一次展示secret、轮换、启停/吊销、查看掩码/最近使用/审计。只存安全摘要，不再次回读secret。旧固定权限模板等价承接，不能API key→super_admin。

恢复 /oauth/token client_credentials，支持旧Basic/form凭据语义、请求scope只能收窄、audience严格验证、HTTPS/可信proxy/CIDR、no-store。短JWT默认旧30分钟、上限60分钟（先核实旧配置并冻结测试），不引入refresh token或授权码OAuth产品。每次机器调用校验enabled/expiry/auth_version，轮换停用即时使旧secret/旧token失效；禁只验JWT签名直到过期。

Direct API Key保持旧只读权限模板，不可调用写接口。机器principal与后台用户分开，写命令身份/审计来自认证结果，不能从payload.operator提权。机器和后台各自鉴权后进同一领域Port。

## 实际旧接口清单（本轮全部登记承接）

固定旧router_registry注册的路径，包括7项GET和11项POST；不是只恢复订单/雷达示例：

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
| POST /api/external/ai-audience/e2e/run | 同上 | 现有受控演练编排；旧composition不可用如实返回，不新建发送旁路 |
| POST /api/external/ai-audience/simple/preview | 同上 | 旧受限simple语义转现有声明式定义 |
| POST /api/external/ai-audience/simple/apply | 同上 | 同上保存；保留retired webhook配置410 |
| POST /api/external/ai-audience/simple/{package_key}/activate | 同上 | 既有人群激活/刷新，River和业务gate |
| POST /api/external/ai-audience/simple/{package_key}/archive | 同上 | 既有归档 |

首次实施冻结每路由请求/响应/错误/权限/数据范围和旧实际注册证据；表中7+11计数正确性以fixture路由清单为准。旧业务prefix gate、publish gate、废弃参数拒绝不能省。受限SQL输入只接受可证明等价翻译的旧允许子集，转换现有声明式规则；绝不将外部SQL直接执行，也不为兼容创建新数据库查询平台。若现有领域缺必要语义，提交具体缺口由根拆小PR补领域Port，禁止用501/伪200充作全项完成。

可以拆PR：机器鉴权+三MCP工具；7GET适配；11POST已有业务适配，但本板块只有全部清单有实际等价结果或有证据的旧本来不可用边界才完成。不能擅自缩成只读平台。

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
