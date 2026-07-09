# dirextalk-connect

Dirextalk-only Matrix bridge for connecting a local coding agent to the current Dirextalk agent room.

This fork keeps the cc-connect agent runtime and Matrix transport, and removes the upstream multi-platform chat integrations. Dirextalk deployment should create a Matrix session for the local `@agent:<server>` user, write a Matrix-only config, and run `dirextalk-connect` against the real `agent_room_id`.

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
curl -L -o dirextalk-connect.tar.gz https://github.com/YingSuiAI/dirextalk-connect/releases/latest/download/dirextalk-connect-v1.3.21-linux-amd64.tar.gz
tar xzf dirextalk-connect.tar.gz
chmod +x dirextalk-connect-v1.3.21-linux-amd64
sudo mv dirextalk-connect-v1.3.21-linux-amd64 /usr/local/bin/dirextalk-connect
```

Build from source:

```bash
git clone https://github.com/YingSuiAI/dirextalk-connect.git
cd dirextalk-connect
make build PLATFORMS_INCLUDE=matrix
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

Run:

```bash
dirextalk-connect --config /path/to/config.toml
```

### Hermes ACP Adapter

Hermes ACP should be launched through the Dirextalk compatibility adapter so reasoning text is buffered and cleaned before it reaches the Matrix room:

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
