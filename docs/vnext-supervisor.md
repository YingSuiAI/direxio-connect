# vNext Supervisor Mode (MC5a)

`schema_version = 2` runs one Connector per `dirextalk-connect` process. It is
an exclusive outbound Agent Control mode: the process does not construct the
legacy Matrix consumer, Engine, schedulers, webhook, bridge, management server,
or local HTTP API. Run several Connectors with separate config files; the
existing per-config process lock, data directory, workspace, credential, boot,
lease, and command cursor remain independent. A host-wide tenant/Connector lock
also rejects a copied identity in another data directory, while a state-path
lock rejects two identities that accidentally share one data directory.

MC5a is a control-plane foundation. It resolves the registered runtime launcher,
binds both that launcher and the Connect executable into the advertised adapter
build digest, establishes mTLS, sends Hello/Ready/heartbeat, and durably applies
exact digest-bound config/stop commands before acknowledging them. Durable State
is the effective configuration across reconnects and restarts. Until AR3 freezes
prompt, checkpoint, result, and evidence frames, MC5a advertises zero available
run capacity with `RUN_PAYLOAD_UNAVAILABLE`; it constructs no legacy Agent and
never claims or executes a `RunAvailable` offer. Do not enable the dormant Legacy
Matrix Gateway consumer as a complete migration until that result path and the
MC4 cutover gate land.

## Configuration

```toml
schema_version = 2
data_dir = "/var/lib/dirextalk/connect/instances/<connector-id>/data"

[instance]
id = "<connector-uuidv7>"
tenant_id = "<tenant-uuidv7>"
host_id = "<host-uuidv7>"
display_name = "Codex primary"
generation = 7
spec_revision = 12

[runtime]
kind = "codex"
adapter = "codex-app-server"
profile = "default"

[control]
node_url = "https://im.example.com"
credential_file = "/run/dirextalk/connect/<connector-id>/credentials/control.credential"
runtime_dir = "/run/dirextalk/connect/<connector-id>/worker"

[routing]
max_concurrent_runs = 2
offline_policy = "queue"

[workspace]
root = "/var/lib/dirextalk/connect/instances/<connector-id>/workspace"

[security]
policy_id = "coding-agent-standard"
secret_refs = ["secret://model/codex-main"]

[limits]
memory_mb = 4096
cpu_quota_percent = 200
processes = 128

[[projects]]
name = "primary"

[projects.agent]
type = "codex"

[projects.agent.options]
work_dir = "/var/lib/dirextalk/connect/instances/<connector-id>/workspace/project"
```

The runtime/adapter pairs are closed: `codex/codex-app-server`,
`openclaw_acp/openclaw-acp`, `eino/eino`, `rig/rig`,
`claude_code/claude-code`, and `custom_acp/vendor-v1`. ACP-backed runtimes use
the existing `acp` project agent; Claude Code uses `claudecode`. Schema v2
rejects every project platform, including Matrix, to make the cutover
structural rather than operator convention. It also rejects command, argument,
environment, provider, and run-as overrides: the registered launchers are fixed
to `codex`, `openclaw-acp`, `eino`, `rig`, `claude`, and `vendor-v1` respectively.

`control.runtime_dir` is a Host Supervisor-created, instance-owned `0700`
directory. On Linux it is the fixed
`/run/dirextalk/connect/<connector-id>/worker` path, beside the root-owned
credential directory. Connect never falls back to a user cache: the identity
lock lives here so copied configs and different service users cannot bypass the
single-process fence.

## Control credential

`control.credential` is one host-supervisor-owned JSON file, at most 64 KiB.
On Unix it must be a regular, non-symlink `0600` or `0440` file. On Windows its
owner and DACL may grant read access only to the process user, SYSTEM, and local
Administrators. The loader rejects unknown/duplicate fields and validates the
Ed25519 key, chain, fingerprint, clientAuth-only EKU, empty CN, validity window,
and the sole URI SAN before opening a network stream.

```json
{
  "schema_version": 2,
  "tenant_id": "<tenant-uuidv7>",
  "connector_id": "<connector-uuidv7>",
  "generation": 7,
  "credential_revision": 11,
  "server_name": "im.example.com",
  "server_root_ca_pem": "-----BEGIN CERTIFICATE-----\n...",
  "connector_issuer_root_ca_pem": "-----BEGIN CERTIFICATE-----\n...",
  "certificate_chain_pem": "-----BEGIN CERTIFICATE-----\n...",
  "private_key_pem": "-----BEGIN PRIVATE KEY-----\n...",
  "leaf_fingerprint_sha256": "<64 lowercase hex characters>"
}
```

Schema v2 is the only writable enrollment output. `server_root_ca_pem` is used
only to authenticate the control server during TLS, while
`connector_issuer_root_ca_pem` is used only to verify the Connector's client
certificate chain. Their pools are never merged. Legacy schema-v1 credentials
with one `root_ca_pem` remain readable only for compatibility and map that
single root to both roles.

`dirextalk-connect enroll` requires one exact lowercase SHA-256 pin alongside
each CA input: `--enrollment-root-ca-sha256`,
`--control-server-root-ca-sha256`, and
`--connector-issuer-root-ca-sha256`. It reads each regular CA file once,
verifies its raw bytes against the pin before TLS or credential processing, and
then passes only those verified bytes onward. A successful enrollment response
must carry `credential_revision == spec_revision` before its credential is
written.

The leaf URI is exactly:

```text
spiffe://dirextalk.internal/v1/tenants/<tenant-id>/connectors/<connector-id>
```

No credential PEM, private key, secret reference value, or config value is
logged. The durable cursor is stored at
`<data_dir>/vnext/connector-state.json` with mode `0600` and an atomic
write/fsync/replace boundary.
