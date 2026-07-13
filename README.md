# dirextalk-connect

Dirextalk bridge for connecting local coding agents to the current Dirextalk system.

Legacy schema-v0 configurations keep the Matrix Agent-room bridge. The new schema-v2 [vNext Supervisor mode](docs/vnext-supervisor.md) runs one isolated outbound Connector control stream per process and structurally disables the legacy Matrix consumer.

The binary is agent-backend neutral. Release builds include all supported local coding agent backends, including ACP-compatible agents, Claude Code, Codex, Gemini, Cursor, Copilot, Qoder, OpenCode, and similar runtimes already present in this repository.

## Install

Via npm:

```bash
npm install -g dirextalk-connect
```

Via Homebrew:

```bash
brew install dirextalk-connect
```

From GitHub Releases:

```bash
curl -L -o dirextalk-connect.tar.gz https://github.com/YingSuiAI/dirextalk-connect/releases/latest/download/dirextalk-connect-v1.3.27-linux-amd64.tar.gz
tar xzf dirextalk-connect.tar.gz
chmod +x dirextalk-connect-v1.3.27-linux-amd64
sudo mv dirextalk-connect-v1.3.27-linux-amd64 /usr/local/bin/dirextalk-connect
```

Build from source:

```bash
git clone https://github.com/YingSuiAI/dirextalk-connect.git
cd dirextalk-connect
make build PLATFORMS_INCLUDE=matrix
```

## Maintainer Release

Do not publish npm alone. Each npm version must have the matching `vX.Y.Z` tag,
GitHub release, and release assets first because npm install downloads the
binary from GitHub Releases.

```bash
bash scripts/release.sh
```

## Matrix Config

`dirextalk-deployer` should generate this file automatically. Manual config is only for local debugging.

```toml
language = "auto"

[speech]
enabled = true
provider = "openai"
language = "zh"

[speech.openai]
api_key = "speech-to-text-api-key"
# base_url = "https://api.openai.com/v1"
# model = "whisper-1"

[[projects]]
name = "dirextalk-agent-room"

[projects.agent]
type = "<agent-backend>"

[projects.agent.options]
work_dir = "/path/to/project"
# Optional: dirextalk-deployer writes these automatically for remote MCP.
mcp_url = "https://example.com/mcp"
mcp_server_name = "dirextalk-example_com"
mcp_agent_token = "dirextalk-agent-token"
mcp_node_id = "agent-node-id"

[[projects.platforms]]
type = "matrix"

[projects.platforms.options]
homeserver = "http://127.0.0.1:8008"
access_token = "agent-matrix-access-token"
user_id = "@agent:example.com"
device_id = "DIREXTALK_CC_CONNECT"
room_id = "!real-agent-room:example.com"
share_session_in_channel = true
group_reply_all = true
auto_join = false
auto_verify = false
```

Set `<agent-backend>` to the local runtime you want to bridge. Supported backend names include `acp`, `claudecode`, `codex`, `gemini`, `cursor`, `copilot`, `qoder`, and `opencode` when those adapters are built into the binary.

### Remote MCP capability

Remote MCP is fail-closed. If any MCP option is present, the server name, URL (or domain), and agent token (or Authorization value) must form a complete canonical configuration. The endpoint must be an absolute HTTPS URL whose path is exactly `/mcp`, with no query or fragment, and authorization must be a non-empty `Bearer` token. Set `mcp_enabled = false` to retain staged values without activating MCP. Every backend must declare its capability explicitly.

| Capability | Backends | Behavior |
| --- | --- | --- |
| `session` | `acp`, `claudecode`, `codex`, `copilot`, `gemini`, `kimi`, `opencode`, `qoder` | Uses the backend's official per-session/process schema. ACP also verifies that the runtime negotiated HTTP MCP support. Temporary credential directories and files are restricted to the current account (plus Windows SYSTEM/Administrators) and removed on normal or failed startup. |
| `host-managed` | `antigravity`, `cursor`, `iflow` | Rejects connect-managed MCP because no verified session/process injection surface exists. Configure MCP in the host runtime outside `dirextalk-connect`. ACP runtimes such as OpenClaw can be restricted with `mcp_capability = "host-managed"`. |
| `unsupported` | `devin`, `pi`, `reasonix`, `tmux` | Rejects remote MCP with an actionable error. The backend remains available for non-MCP use. |

Run:

```bash
dirextalk-connect --config /path/to/config.toml
```

### Hermes ACP Adapter

Hermes ACP should be launched through the Dirextalk compatibility adapter so reasoning text is buffered and cleaned before it reaches the Matrix room:

This adapter owns only the conversation bridge. Hermes currently does not advertise HTTP MCP support in ACP initialization, so its service-scoped native profile `mcp_servers` registry owns MCP; do not send per-session MCP data through the adapter unless Hermes later negotiates `mcpCapabilities.http = true`.

```toml
[projects.agent]
type = "acp"

[projects.agent.options]
work_dir = "/path/to/project"
cmd = "dirextalk-connect"
args = ["hermes-acp-adapter", "--", "hermes", "acp"]
display_name = "Hermes ACP"
```

Install as a daemon:

```bash
dirextalk-connect daemon install --config /path/to/config.toml --force
```

For multiple Dirextalk nodes on one machine, give each daemon a distinct service name:

```bash
dirextalk-connect daemon install --config /path/to/t1/config.toml --service-name t1.dirextalk.ai --force
dirextalk-connect daemon status --service-name t1.dirextalk.ai
```

## Dirextalk Requirements

- The Matrix user must be the local `@agent:<server>` identity, not the portal owner.
- `room_id` is required and must be the real persisted Dirextalk `agent_room_id`; the bridge rejects legacy pseudo ids such as `!agent:<domain>`.
- Only `type = "matrix"` is supported.
- Voice messages require `[speech]` with a working speech-to-text provider key. After transcription, the text is sent through the same agent-room conversation path as a normal text message.
- Other chat platforms from upstream cc-connect are intentionally removed.
