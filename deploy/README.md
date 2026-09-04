# AI-CRM v3 deployment

The production process listens only on `127.0.0.1:8080`. Caddy owns public
ports 80/443 and proxies `id-dev.youcangogogo.com` to that loopback listener.
PostgreSQL 16 is the only runtime data dependency.

## Runtime layout

- immutable releases: `/opt/aicrm/releases/<40-char-git-sha>`
- active symlink: `/opt/aicrm/current`
- secrets and runtime settings: `/etc/aicrm/aicrm.env` (`0640`, never in Git)
- API: `aicrm.service`
- migrations: `aicrm-migrate.service` (oneshot before a release restart)
- Automation Operations defaults: `aicrm-automation-bootstrap.service` (idempotent oneshot after API and River readiness)
- WeCom inbox: `aicrm-wecom-worker.service` plus external systemd timer
- durable River jobs: `aicrm-effects-worker.service` (External Effects and customer directory queues)
- Daily customer reconciliation creation: `aicrm-customer-sync-daily.service` plus the 02:30 Asia/Shanghai systemd timer

The app has `api`, `worker`, and `effects-worker` roles. The oneshot worker runs
one bounded callback-inbox claim or creates a scheduled customer-sync run. The
long-running effects worker is the single River runtime for both durable queues;
there is no ticker or scheduler inside a domain package.

## Automated release

The `deploy` job runs only after the required `check` job succeeds on `main`.
It builds static Linux binaries, uploads one archive over pinned-host SSH and
runs `install-release.sh`. The installer applies forward-only migrations,
atomically switches `/opt/aicrm/current`, restarts the API and checks `/readyz`.
If migration, restart or readiness fails, the active symlink and service return
to the previous binary. Database migrations are forward-compatible and are not
destructively rolled back.

After the API and durable worker are ready, every release runs the Automation
Operations semantic bootstrap. It creates only paused 7/30/90-day canonical
customer audiences and queues their initial River snapshots. It does not copy
legacy rows or create outbound effects, and it preserves later operator edits.

Required GitHub Actions secrets:

- `DEPLOY_HOST`
- `DEPLOY_USER`
- `DEPLOY_SSH_KEY`
- `DEPLOY_KNOWN_HOSTS` (the verified ED25519 host-key line, not a fingerprint alone)

## Provider boundary

`AICRM_WECOM_ENABLED=false` and `AICRM_WECOM_CUSTOMER_SYNC_ENABLED=false` are safe defaults. Enabling WeCom requires the
Corp ID, Agent ID, application Secret, callback Token, callback EncodingAESKey
and a separate context signing key to be present together. Customer directory
sync additionally requires its own customer-contact Secret. Alipay has no live
runtime configuration in this release.

AI Assistant has three independent switches. `AICRM_AI_ASSISTANT_UI_ENABLED`
serves the frozen two-level review UI; signed machine intake additionally needs
an integration key, a 32-byte-or-longer secret and an internal actor ID. Real
private-message dispatch requires all of
`AICRM_AI_ASSISTANT_DISPATCH_ENABLED=true`,
`AICRM_OUTBOUND_PROVIDER_ENABLED=true`, enabled WeCom contact credentials and
`AICRM_AI_ASSISTANT_PROVIDER_PERMISSION=private-message-authorized`. Approval
creates durable External Effects; Provider acceptance is shown separately from
delivery proof, and ambiguous outcomes require the fenced reconciliation API.

The phone migration command never connects to the source host. Export a minimal
snapshot through a separately authorized read-only channel, compute its SHA-256,
then run `inspect`, `dry-run`, `apply --confirm-apply`, and `reconcile` in order.
Do not place snapshots, source credentials, raw phones, external user IDs, or
command output containing them in Git or structured logs.

## Acceptance

```bash
curl --fail --silent --show-error http://127.0.0.1:8080/healthz
curl --fail --silent --show-error http://127.0.0.1:8080/readyz
curl --fail --silent --show-error https://id-dev.youcangogogo.com/readyz
```
