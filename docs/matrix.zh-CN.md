# Dirextalk Matrix 桥接

`dirextalk-connect` 只支持 Dirextalk Matrix agent room。

不要配置公共 Matrix 账号，也不要使用个人 Element access token。Dirextalk message server 必须通过 `agent.matrix_session.create` 为本地 `@agent:<server>` 用户创建 Matrix Client-Server session。

## 必要配置

`dirextalk-deployer` 会自动写入配置：

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
# 可选：用于 owner 范围审批卡片的精确 portal owner Matrix MXID。
# approval_owner_id = "@owner:example.com"
share_session_in_channel = true
group_reply_all = true
auto_join = false
auto_verify = false
```

将 `<agent-backend>` 替换为要桥接的本地运行时，例如 `acp`、`claudecode`、`codex`、`gemini`、`cursor`、`copilot`、`qoder`、`opencode`。

## 运行规则

- `room_id` 必填，且必须是真实的 Dirextalk `agent_room_id`。
- 非 `room_id` 房间的事件会被忽略。
- 回复必须由 Matrix `@agent:<server>` 用户发送。
- 不能使用 portal owner session 发送 agent 回复。
- 配置校验只接受 `type = "matrix"`。

## Owner 范围审批卡片

配置精确的 portal owner Matrix MXID `approval_owner_id` 后，才会启用客户端审批卡片。它与 `allow_from` 完全独立，绝不会根据发送者自动推断；缺失时该桥接保持关闭，以兼容既有配置。

桥接使用 `m.room.message`，自定义 `msgtype` 分别为 `io.dirextalk.agent.approval.request`、`io.dirextalk.agent.approval.response` 与 `io.dirextalk.agent.approval.result`。结构化字段只能放在 `io.dirextalk.agent_approval` map 中，`body` 仅是非敏感回退文本。response 必须同时来自配置的房间和精确 `approval_owner_id`，且只能携带不透明 `approval_id` 与 `allow` 或 `deny`。Connect 不会暴露后端 request ID、session key、命令文本、工具输入、路径或凭据。重复或过期 response 对 agent 没有副作用；Connect 能回复时会返回 `outcome = "expired"`。result 的 `code` 只会在 `outcome = "failed"` 时出现，且仅允许 `backend_response_failed`、`session_unavailable` 与 `invalid_response`。
