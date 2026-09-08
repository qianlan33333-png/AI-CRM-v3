# OperationCycle local Runner installation contract

This package is not enabled by the CRM web process and does not select a machine,
workspace, Codex socket, strategy, or action. An operator installs it only after
the designated machine, HTTPS CRM endpoint, exact Codex version, managed app-server
socket, and local binding directories have been reviewed.

Build the two local binaries from the reviewed release:

```sh
go build -o aicrm-operation-cycle-runner ./cmd/operation-cycle-runner
go build -o aicrm-operation-cycle-result ./cmd/operation-cycle-result
```

The `-o` names are the release artifacts referenced in the generated prompt;
installation must place both names on the local task PATH.

A service manager starts `aicrm-operation-cycle-runner` with explicit absolute
paths, a registered runner id, an HTTPS CRM URL, and each reviewed binding:

```sh
exec aicrm-operation-cycle-runner \
  --crm-url "$AICRM_OPERATION_RUNNER_URL" \
  --service-token-env AICRM_OPERATION_RUNNER_SERVICE_TOKEN \
  --runner-id "$AICRM_OPERATION_RUNNER_ID" \
  --codex-binary "$AICRM_CODEX_BINARY" \
  --codex-socket "$AICRM_CODEX_APP_SERVER_SOCKET" \
  --codex-version "$AICRM_CODEX_EXPECTED_VERSION" \
  --control-socket "$AICRM_OPERATION_RUNNER_CONTROL_SOCKET" \
  --renewal-interval 25s \
  --binding excel_workspace="$AICRM_EXCEL_WORKSPACE"
```

The service token is read only from the named protected service environment. The
command does not print the token, endpoint credentials, local bindings, prompts,
or completion payload. Never put a token in arguments, a unit file, a result
file, or a runner heartbeat.

At startup, the connector verifies the absolute Codex binary's exact version,
that the app-server path is a connectable Unix socket, then completes the
`initialize` request followed by its `initialized` notification before it
heartbeats or claims. It then heartbeats, claims at most one action, and renews its
fenced 60-second lease every 25 seconds while the local control socket is alive.
`SIGTERM` or `SIGINT` closes that socket and stops the process; a restart can
reclaim only its own expired action. A recovered action missing either thread or
turn binding records `start_outcome_unknown` for manual verification and never
starts a replacement task.

The generated task prompt names the frozen objective, instructions, context
hashes, and approved local bindings. After human review of a sanitized aggregate
result, the local task invokes:

```sh
aicrm-operation-cycle-result --socket "/the-reviewed/control/socket" \
  --request-id REQUEST_ID --result-file /absolute/path/to/safe-result.json
```

The result command talks only to the local socket. It does not contact CRM
directly. The socket accepts only the action currently held by this process and
records a fenced terminal event through the runner. A missing 0104 execution
snapshot is terminally marked `missing_execution_snapshot` with manual review;
it is never reconstructed from a later strategy or run version.

No deployment action, real Codex thread/turn, or customer effect is part of this
runbook. Validate a target installation first with the controlled HTTP and Unix
socket tests in this repository, then use the explicit production change process.
