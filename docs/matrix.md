# Dirextalk Matrix Bridge

`dirextalk-connect` only supports the Dirextalk Matrix agent room.

Do not configure a public Matrix account or a personal Element access token. The Dirextalk message server must create a Matrix Client-Server session for the local `@agent:<server>` user through `agent.matrix_session.create`.

## Required Config

`dirextalk-deployer` writes the config automatically:

```toml
[[projects]]
name = "dirextalk-agent-room"

[projects.agent]
type = "<agent-backend>"

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

Replace `<agent-backend>` with the local runtime to bridge, for example `acp`, `claudecode`, `codex`, `gemini`, `cursor`, `copilot`, `qoder`, or `opencode`.

## Runtime Rules

- `room_id` is mandatory and must be the real Dirextalk `agent_room_id`.
- Events outside `room_id` are ignored.
- Replies are sent as the Matrix `@agent:<server>` user.
- The portal owner session must not be used for agent replies.
- Only `type = "matrix"` is accepted by config validation.
