# Production audience protected snapshot

Classification: OneID involved in future member assignment; this command only
preserves source identities and their explicitly supplied scope declarations.
Persistence: encrypted local snapshot and source read-only PostgreSQL transaction.
Provider effects: none. No identities, audience packages, refresh jobs, member
entered events, leases, timers or outbound effects are created.

`cmd/migrate-audience-history` implements `extract` and `inspect` only. It does
not implement or imply apply, refresh, source SQL execution, or completed cutover.
Source is the real singular `ai_audience_package_group`, `ai_audience_package`,
`ai_audience_package_version`, `ai_audience_member_current` contract. It preserves
all fields via JSONB, including historical natural-language definitions/prompts,
parameters, original SQL text, all 83 versions and both active/exited membership.
Counts here describe the coordinator's source inspection, not a capture receipt:
38 packages (6 active, 32 archived), 83 versions, 29,248 members (14,556 active,
14,692 exited); 80 versions have no template key. Extraction independently checks
counts/digests rather than trusting these numbers.

## Execution

Prepare a cryptographically random 32-byte key, raw standard-base64 encoded,
in a separate 0600 file. Keep snapshot and key outside git and logs. On the
source service host, pass its effective source DB URL through an environment
variable, never a command-line URL. Use an existing protected connection.

```
migrate-audience-history extract \
  --source-url-env AICRM_AUDIENCE_SOURCE_DATABASE_URL \
  --source-system aicrm-v2-production \
  --snapshot /protected/audience.enc \
  --snapshot-key-file /separate/audience.key
migrate-audience-history inspect \
  --snapshot /protected/audience.enc \
  --snapshot-key-file /separate/audience.key \
  --expected-sha256 <digest-returned-by-extract>
```

Optional `--declared-corp-scope` and `--declared-unionid-scope` preserve an
operator-supplied declaration only. Omitted scope is explicitly empty. Every
snapshot has `scope_verified=false` and `executable=false`; inspect rejects a
snapshot claiming otherwise. A declaration is not sufficient verified OneID
provenance. Output includes only fixed count buckets, capture time and digest;
no names, scopes, identities, definitions, SQL, or raw DB errors.

Snapshot uses unique magic + AES-256-GCM authenticated encryption. Every row and
table has a digest, source IDs are unique and ordered, required group/package/
current-version associations are checked, and a current version belonging to
another package fails closed. Output is 0600, exclusive creation, fsynced. An
interrupted write is removed. Plaintext never lands on disk. Do not execute any
historical SQL, including compiled SQL; even inspection merely hashes it.

## Apply assessment

Current Segment ports expose PackageReader/SnapshotReader and source resolver
interfaces, but no public historical package/snapshot importer that preserves
this complete legacy contract. Existing automation migration also migrates
unrelated agents/bindings and assumes a different source schema. Do not reuse it
by manufacturing the missing tables or dropping versions.

Future owner import must retain unsupported definitions as read-only history,
map canonical supported templates explicitly, resolve scoped authoritative
identity facts through Identity Port, quarantine ambiguity, and persist source
mapping/digests in one owner UoW. Preserve active/exited source history; only
mapped active members contribute to current snapshots. Target packages start
paused, auto-refresh off. Source lease tokens/schedule execution fields remain
historical evidence and must never become live target leases or runnable jobs.
Exact replay needs zero additions; changed source rows require explicit CAS and
must not overwrite newer native V3 edits. This command intentionally contains no
such apply path.

## Verification

Unit/race tests cover authenticated encryption, tamper/wrong-key rejection,
precision of large JSON integers, preservation of SQL as inert text, no sensitive
summary output, exact digest, file permissions, overwrite/symlink rejection,
foreign-key/source drift and unsupported activation flags. Isolated PostgreSQL
integration captures real tables and verifies a hostile source view attempting
INSERT fails inside READ ONLY, with zero inserted rows. No production capture
has been executed by this implementation task.
