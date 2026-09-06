# 开放平台旧机器路由完整盘点

固定供体dd8d60d。来源：docs/architecture/route_ownership_manifest.yml中external_integration + api_client_jwt，配合Python实际路由装饰器定位。清单是兼容验收输入，首次实施须再核对router_registry/main是否真实注册；manifest本身不能证明可运行。human_session和内部worker路由不擅自开放。

共56个方法/路径条目（含MCP）。/api/external前缀的18项仅为其中一部分。

| 方法 | 路径 | 旧权限 | 实际源码 |
|---|---|---|---|
| GET | `/mcp` | `mcp_read` | `aicrm_next/channels/integration_gateway/api.py:13; aicrm_next/channels/integration_gateway/api.py:18` |
| POST | `/mcp` | `mcp_execute` | `aicrm_next/channels/integration_gateway/api.py:13; aicrm_next/channels/integration_gateway/api.py:18` |
| GET | `/api/identity/resolve` | `identity_resolve` | `aicrm_next/crm/identity_contact/api.py:48` |
| GET | `/api/external/chat-records` | `external_read` | `aicrm_next/extensions/archive/message_archive/api.py:71` |
| GET | `/api/external/questionnaire-submissions` | `external_read` | `aicrm_next/extensions/forms/questionnaire/api.py:320` |
| GET | `/api/external/radar-clicks` | `external_read` | `aicrm_next/extensions/radar/radar_links/api.py:280` |
| GET | `/api/external/radar-links` | `external_read` | `aicrm_next/extensions/radar/radar_links/api.py:332` |
| POST | `/api/external/ai-audience/spec/dry-run` | `external_write` | `aicrm_next/extensions/ai/ai_audience_ops/external_api.py:74` |
| POST | `/api/external/ai-audience/spec/apply` | `external_write` | `aicrm_next/extensions/ai/ai_audience_ops/external_api.py:89` |
| POST | `/api/external/ai-audience/spec/publish` | `external_write` | `aicrm_next/extensions/ai/ai_audience_ops/external_api.py:126` |
| POST | `/api/external/ai-audience/packages/{package_key}/archive` | `external_write` | `aicrm_next/extensions/ai/ai_audience_ops/external_api.py:175` |
| POST | `/api/external/ai-audience/templates/preview` | `external_write` | `aicrm_next/extensions/ai/ai_audience_ops/external_api.py:33` |
| POST | `/api/external/ai-audience/templates/apply` | `external_write` | `aicrm_next/extensions/ai/ai_audience_ops/external_api.py:55` |
| POST | `/api/external/ai-audience/simple/preview` | `external_write` | `aicrm_next/extensions/ai/ai_audience_ops/external_api.py:234` |
| POST | `/api/external/ai-audience/simple/apply` | `external_write` | `aicrm_next/extensions/ai/ai_audience_ops/external_api.py:249` |
| POST | `/api/external/ai-audience/simple/{package_key}/activate` | `external_write` | `aicrm_next/extensions/ai/ai_audience_ops/external_api.py:286` |
| POST | `/api/external/ai-audience/simple/{package_key}/archive` | `external_write` | `aicrm_next/extensions/ai/ai_audience_ops/external_api.py:327` |
| POST | `/api/external/ai-audience/e2e/run` | `external_write` | `aicrm_next/extensions/ai/ai_audience_ops/external_api.py:210` |
| POST | `/api/automation/group-ops/broadcast` | `group_broadcast_execute` | `aicrm_next/automation/automation_engine/group_ops/api.py:660` |
| GET | `/api/external/orders` | `external_read` | `aicrm_next/extensions/commerce/commerce/external_orders.py:120` |
| GET | `/api/external/orders/{order_no}` | `external_read` | `aicrm_next/extensions/commerce/commerce/external_orders.py:182` |
| GET | `/api/external/users/resolve` | `external_read` | `aicrm_next/extensions/commerce/commerce/external_orders.py:278` |
| POST | `/api/ai-assist/external/campaigns` | `campaign_draft_create` | `aicrm_next/extensions/ai/ai_assist/api.py:68` |
| GET | `/api/ai-assist/external/campaigns/{campaign_code}` | `campaign_status_read` | `aicrm_next/extensions/ai/ai_assist/api.py:80` |
| POST | `/api/ai-assist/external/campaign-preparations` | `campaign_preparation_create` | `aicrm_next/extensions/ai/ai_assist/api.py:85` |
| GET | `/api/ai-assist/external/campaign-preparations/{preparation_id}` | `campaign_preparation_read` | `aicrm_next/extensions/ai/ai_assist/api.py:103` |
| POST | `/api/ai-assist/external/campaign-preparations/{preparation_id}/commit` | `campaign_preparation_commit` | `aicrm_next/extensions/ai/ai_assist/api.py:113` |
| GET | `/api/ai/audience/schema-catalog` | `external_read` | `aicrm_next/extensions/ai/ai_audience_ops/api.py:54` |
| GET | `/api/ai/audience/packages` | `external_read` | `aicrm_next/extensions/ai/ai_audience_ops/api.py:59; aicrm_next/extensions/ai/ai_audience_ops/api.py:64` |
| POST | `/api/ai/audience/packages` | `external_write` | `aicrm_next/extensions/ai/ai_audience_ops/api.py:59; aicrm_next/extensions/ai/ai_audience_ops/api.py:64` |
| GET | `/api/ai/audience/packages/{package_id}` | `external_read` | `aicrm_next/extensions/ai/ai_audience_ops/api.py:74` |
| POST | `/api/ai/audience/packages/{package_id}/versions` | `external_write` | `aicrm_next/extensions/ai/ai_audience_ops/api.py:80` |
| POST | `/api/ai/audience/packages/{package_id}/preview` | `external_write` | `aicrm_next/extensions/ai/ai_audience_ops/api.py:89` |
| POST | `/api/ai/audience/packages/{package_id}/publish` | `external_write` | `aicrm_next/extensions/ai/ai_audience_ops/api.py:99` |
| POST | `/api/ai/audience/packages/{package_id}/pause` | `external_write` | `aicrm_next/extensions/ai/ai_audience_ops/api.py:109` |
| POST | `/api/ai/audience/packages/{package_id}/archive` | `external_write` | `aicrm_next/extensions/ai/ai_audience_ops/api.py:116` |
| POST | `/api/ai/audience/packages/{package_id}/refresh` | `external_write` | `aicrm_next/extensions/ai/ai_audience_ops/api.py:123` |
| POST | `/api/ai/audience/ticks/incremental` | `external_write` | `aicrm_next/extensions/ai/ai_audience_ops/api.py:145` |
| POST | `/api/ai/audience/ticks/daily` | `external_write` | `aicrm_next/extensions/ai/ai_audience_ops/api.py:150` |
| POST | `/api/ai/audience/source-dirty` | `external_write` | `aicrm_next/extensions/ai/ai_audience_ops/api.py:155` |
| GET | `/api/ai/audience/packages/{package_id}/outbound-subscriptions` | `external_read` | `aicrm_next/extensions/ai/ai_audience_ops/api.py:164; aicrm_next/extensions/ai/ai_audience_ops/api.py:170` |
| POST | `/api/ai/audience/packages/{package_id}/outbound-subscriptions` | `external_write` | `aicrm_next/extensions/ai/ai_audience_ops/api.py:164; aicrm_next/extensions/ai/ai_audience_ops/api.py:170` |
| PATCH | `/api/ai/audience/outbound-subscriptions/{subscription_id}` | `external_write` | `aicrm_next/extensions/ai/ai_audience_ops/api.py:176` |
| POST | `/api/ai/audience/outbound-subscriptions/{subscription_id}/pause` | `external_write` | `aicrm_next/extensions/ai/ai_audience_ops/api.py:182` |
| GET | `/api/ai/audience/packages/{package_id}/runs` | `external_read` | `aicrm_next/extensions/ai/ai_audience_ops/api.py:221` |
| GET | `/api/ai/audience/packages/{package_id}/members` | `external_read` | `aicrm_next/extensions/ai/ai_audience_ops/api.py:226` |
| GET | `/api/ai/audience/packages/{package_id}/events` | `external_read` | `aicrm_next/extensions/ai/ai_audience_ops/api.py:231` |
| GET | `/api/ai/audience/packages/{package_id}/external-effects` | `external_read` | `aicrm_next/extensions/ai/ai_audience_ops/api.py:236` |
| GET | `/api/ai/audience/health` | `external_read` | `aicrm_next/extensions/ai/ai_audience_ops/api.py:241` |
| POST | `/api/operation-cycles/reports` | `operation_cycle_report_write` | `aicrm_next/extensions/hxc/operation_cycles/api.py:188` |
| POST | `/api/operation-cycles/runner/heartbeat` | `operation_cycle_runner_heartbeat` | `aicrm_next/extensions/hxc/operation_cycles/api.py:334` |
| POST | `/api/operation-cycles/action-requests/claim` | `operation_cycle_action_claim` | `aicrm_next/extensions/hxc/operation_cycles/api.py:357` |
| POST | `/api/operation-cycles/action-requests/{request_id}/events` | `operation_cycle_action_event_write` | `aicrm_next/extensions/hxc/operation_cycles/api.py:385` |
| GET | `/api/operation-cycles/context-index` | `operation_cycle_context_read` | `aicrm_next/extensions/hxc/operation_cycles/api.py:407` |
| GET | `/api/operation-cycles/strategies/{strategy_key}/context` | `operation_cycle_context_read` | `aicrm_next/extensions/hxc/operation_cycles/api.py:417` |
| POST | `/api/operation-cycles/strategy-change-proposals` | `operation_cycle_strategy_propose` | `aicrm_next/extensions/hxc/operation_cycles/api.py:458` |

## 执行规则

保留等价已有业务，只通过Access机器授权和领域Port接通；不是将所有后台路由外放。逐条标已覆盖/待适配/旧实际不可用（须证据）。运营周期已有专用Bearer与AI助手已有HMAC不得移除，通用身份接入不改变审批、幂等和Provider gate。

旧外部SQL/执行器/定时tick输入必须转换为既有声明式规则、现有River受理或已有业务命令，不直跑SQL、不由HTTP直接drain EER Worker。输入不能安全等价承接时明确列缺口给根审核，不以501或伪成功算完成。
