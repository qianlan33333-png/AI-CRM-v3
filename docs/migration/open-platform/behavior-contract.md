# Open platform behavior contract

## Classification

```text
OneID: resolves scoped external identity and reads canonical customer; never provisions, links, or merges.
Persistence: Access machine clients/grants/audit use one local PostgreSQL Unit of Work.
External Effects: no new effect writer. Every business command reaches its existing domain Port/UoW/River/EER path.
```

## Frozen sources and V3 disposition

| Frozen dd8d60d source | Behavior retained | V3 seam | Status |
| --- | --- | --- | --- |
| `platform/platform_foundation/auth_platform/{api,models,profiles,service,repository,credentials,client_authentication}.py` | Basic/form `client_credentials`, 1800 default, 60–3600 TTL, audience/scope/CIDR validation and disabled/expiry rejection | `internal/access/app.MachineService`, `internal/access/store`, migration 0096 | Go-equivalent implemented; composition key and route adapter pending |
| `platform/admin_config/api_clients.py` | client list/create/one-time secret/rotate/enable/disable and masked recent-use state | Access machine client admin API | Implemented; V3 host page pending |
| `platform/admin_config/direct_api_key{,_api}.py` | one fixed Bearer direct key limited to `external_read` | `direct_external_api_key` template in Access | Implemented; V3 host page pending |
| `channels/integration_gateway/{api,mcp,dispatch}.py`, `mcp_tool_catalog.py`, `mcp_composition.py` | JSON-RPC 2024-11-05, initialize/list/call, only resolve_customer/get_customer_context/get_recent_messages | `internal/openplatform/http`, composition-owned executor | Protocol/auth/tool catalog implemented; canonical customer/archive Port adapter pending |
| `crm/identity_contact/api.py` | scoped identity resolve without implicit customer creation | Identity `port.Resolver` | Adapter pending |
| `extensions/{archive,message_archive,forms,radar,commerce}/...` | seven external GET reads | owner Port adapters | Adapter pending |
| `extensions/ai/ai_audience_ops/{external_api,api}.py` | 11 legacy paths and audience catalog/package/run paths, including publish and retired webhook gate | existing segment/automation/AI Ports | Adapter pending; no raw SQL route will be added |
| `automation/automation_engine/group_ops/api.py`, `extensions/ai/ai_assist/api.py`, `extensions/hxc/operation_cycles/api.py` | broadcast, AI campaign, and operation-cycle routes | existing GroupOps/AI Assistant/OperationCycle Ports | Adapter pending; existing approval, idempotency, River, and Provider gates remain authoritative |

## Route inventory

`internal/openplatform/http/inventory.go` has the authoritative 56 `method + path + capability` records transcribed from the approved `03a-machine-route-inventory.md`. The HTTP mux registers every record explicitly; it does not open a prefix or an admin route by accident. A record is complete only after the composition executor maps it to an existing owner Port and its behavior test passes.

## Security invariants

- Secrets are shown only in create/rotate responses, are stored as Argon2id hashes, and do not appear in summaries or audit details.
- `auth_version`, enabled state, expiry, audience, requested-scope subset, and source CIDR are checked against PostgreSQL on every machine request. A JWT signature alone is insufficient.
- The direct key only accepts the fixed readonly template. A machine principal has capabilities; it cannot become an Access `super_admin` or use a payload `operator` value as authority.
- Untrusted `X-Forwarded-*` headers are ignored. TLS is required unless the remote address belongs to an explicitly configured trusted-proxy CIDR and asserts HTTPS.
- Historical import receipts have no secret/token fields. Non-verifiable source credentials are recorded `inactive` or `reissue_required`; they never become live credentials.
