# dirextalk-connect Install

`dirextalk-connect` is distributed for Dirextalk only. The supported bridge target is the Dirextalk Matrix agent room.

## Recommended

Use `dirextalk-deployer`. It calls Dirextalk `agent.matrix_session.create`, writes the Matrix-only config, and installs the local daemon.

## Manual Install

Via npm:

```bash
npm install -g dirextalk-connect
```

Via Homebrew:

```bash
brew install dirextalk-connect
```

Download binary from GitHub Releases:

```bash
curl -L -o dirextalk-connect.tar.gz https://github.com/YingSuiAI/dirextalk-connect/releases/latest/download/dirextalk-connect-v1.3.18-linux-amd64.tar.gz
tar xzf dirextalk-connect.tar.gz
chmod +x dirextalk-connect-v1.3.18-linux-amd64
sudo mv dirextalk-connect-v1.3.18-linux-amd64 /usr/local/bin/dirextalk-connect
```

Build from source:

```bash
git clone https://github.com/YingSuiAI/dirextalk-connect.git
cd cc-connect
make build AGENTS=codex PLATFORMS_INCLUDE=matrix
```

## Config

```toml
[[projects]]
name = "dirextalk-agent-room"

[projects.agent]
type = "codex"

[projects.agent.options]
work_dir = "/path/to/project"

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

`room_id` is required and must be the real persisted Dirextalk `agent_room_id`. Legacy pseudo ids such as `!agent:<domain>` are rejected.

Start foreground:

```bash
dirextalk-connect --config /path/to/config.toml
```

## Hermes ACP Adapter

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

Install daemon:

```bash
dirextalk-connect daemon install --config /path/to/config.toml --force
```

Install a named daemon for one Dirextalk node:

```bash
dirextalk-connect daemon install --config /path/to/t1/config.toml --service-name t1.dirextalk.ai --force
```

Check daemon:

```bash
dirextalk-connect daemon status
dirextalk-connect daemon status --service-name t1.dirextalk.ai
dirextalk-connect daemon logs -n 100
```
