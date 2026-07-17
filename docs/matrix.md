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
# Optional: exact Matrix owner MXID for owner-scoped approval cards.
# approval_owner_id = "@owner:example.com"
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

## Owner-Scoped Approval Cards

Set `approval_owner_id` to the exact portal-owner Matrix MXID to enable client approval cards. It is intentionally separate from `allow_from` and is never inferred from a sender. When absent, this bridge is disabled for compatibility.

The bridge uses `m.room.message` with these custom `msgtype` values: `io.dirextalk.agent.approval.request`, `io.dirextalk.agent.approval.response`, and `io.dirextalk.agent.approval.result`. Structured fields are exclusively inside the `io.dirextalk.agent_approval` map; `body` is only a non-sensitive fallback. A response must come from both the configured room and exact `approval_owner_id`, and contains only the opaque `approval_id` plus `allow` or `deny`. Connect does not expose backend request IDs, session keys, command text, tool input, paths, or credentials. Duplicate or stale responses have no agent side effect and receive `outcome = "expired"` when Connect can reply. Result `code` is omitted unless `outcome = "failed"`; its only values are `backend_response_failed`, `session_unavailable`, and `invalid_response`.
