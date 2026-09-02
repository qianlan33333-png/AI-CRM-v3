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
- WeCom inbox: `aicrm-wecom-worker.service` plus external systemd timer

The app has only `api` and `worker` roles. The worker runs one bounded inbox
claim and exits; there is no ticker or scheduler inside the Go process.

## Automated release

The `deploy` job runs only after the required `check` job succeeds on `main`.
It builds static Linux binaries, uploads one archive over pinned-host SSH and
runs `install-release.sh`. The installer applies forward-only migrations,
atomically switches `/opt/aicrm/current`, restarts the API and checks `/readyz`.
If migration, restart or readiness fails, the active symlink and service return
to the previous binary. Database migrations are forward-compatible and are not
destructively rolled back.

Required GitHub Actions secrets:

- `DEPLOY_HOST`
- `DEPLOY_USER`
- `DEPLOY_SSH_KEY`
- `DEPLOY_KNOWN_HOSTS` (the verified ED25519 host-key line, not a fingerprint alone)

## Provider boundary

`AICRM_WECOM_ENABLED=false` is the safe default. Enabling WeCom requires the
Corp ID, Agent ID, application Secret, callback Token, callback EncodingAESKey
and a separate context signing key to be present together. Alipay has no live
runtime configuration in this release.

## Acceptance

```bash
curl --fail --silent --show-error http://127.0.0.1:8080/healthz
curl --fail --silent --show-error http://127.0.0.1:8080/readyz
curl --fail --silent --show-error https://id-dev.youcangogogo.com/readyz
```
