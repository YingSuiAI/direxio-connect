# dirextalk-connect

Dirextalk 专用 Matrix 桥接器，用于把本地 coding agent 接入当前 Dirextalk agent room。

这个分支保留 cc-connect 的 agent runtime 和 Matrix transport，删除上游多聊天平台集成。Dirextalk 部署流程应该为本地 `@agent:<server>` 用户创建 Matrix session，写入 Matrix-only 配置，并让 `dirextalk-connect` 只监听真实的 `agent_room_id`。

发布二进制必须保持 agent 后端中立，默认包含本仓库已有的所有本地 coding agent 后端，包括 ACP 兼容 agent、Claude Code、Codex、Gemini、Cursor、Copilot、Qoder、OpenCode 等运行时。

## 安装

npm:

```bash
npm install -g dirextalk-connect
```

Homebrew:

```bash
brew install dirextalk-connect
```

GitHub Releases:

```bash
curl -L -o dirextalk-connect.tar.gz https://github.com/YingSuiAI/dirextalk-connect/releases/latest/download/dirextalk-connect-v1.3.20-linux-amd64.tar.gz
tar xzf dirextalk-connect.tar.gz
chmod +x dirextalk-connect-v1.3.20-linux-amd64
sudo mv dirextalk-connect-v1.3.20-linux-amd64 /usr/local/bin/dirextalk-connect
```

源码构建:

```bash
git clone https://github.com/YingSuiAI/dirextalk-connect.git
cd dirextalk-connect
make build PLATFORMS_INCLUDE=matrix
```

## Matrix 配置

`dirextalk-deployer` 会自动生成配置。手动配置仅用于本地调试。

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

将 `<agent-backend>` 替换为要桥接的本地运行时。默认发布构建会包含已有适配器，例如 `acp`、`claudecode`、`codex`、`gemini`、`cursor`、`copilot`、`qoder`、`opencode`。

### 远端 MCP 能力

远端 MCP 采用 fail-closed。只要出现任一 MCP 配置项，server name、URL（或 domain）与 agent token（或 Authorization 值）就必须组成完整 canonical 配置。endpoint 必须是绝对 HTTPS URL，路径精确为 `/mcp`，不得带 query、fragment 或 userinfo；认证必须是非空 `Bearer` token。可设置 `mcp_enabled = false` 暂存配置而不启用。每个 backend 都必须显式声明能力。

| 能力 | Backend | 行为 |
| --- | --- | --- |
| `session` | `acp`、`claudecode`、`codex`、`copilot`、`gemini`、`kimi`、`opencode`、`qoder` | 使用该 backend 官方的会话/进程级 schema。ACP 还必须协商得到 HTTP MCP 支持。临时凭据目录仅允许当前账号访问（Windows 另含 SYSTEM/Administrators），正常或失败启动后都会清理。 |
| `host-managed` | `antigravity`、`cursor`、`iflow`，以及 OpenClaw/Hermes 宿主 | connect 不注入 MCP，由宿主原生 registry/profile 管理。宿主启动 Codex 等 child 不改变所有权。 |
| `unsupported` | `devin`、`pi`、`reasonix`、`tmux` | 启用远端 MCP 时给出可操作错误；不使用 MCP 时 backend 仍可运行。 |

运行:

```bash
dirextalk-connect --config /path/to/config.toml
```

### Hermes ACP Adapter

Hermes ACP 应通过 Dirextalk 兼容层启动，这样推理文本会先被缓存和清洗，不会直接进入 Matrix agent room：

该 adapter 只负责会话桥接。Hermes 当前在 ACP initialize 中没有声明 HTTP MCP 支持，因此 MCP 必须由服务隔离 profile 的原生 `mcp_servers` registry 管理；只有未来 Hermes 协商得到 `mcpCapabilities.http = true` 后，才可改用会话注入。

```toml
[projects.agent]
type = "acp"

[projects.agent.options]
work_dir = "/path/to/project"
cmd = "dirextalk-connect"
args = ["hermes-acp-adapter", "--", "hermes", "acp"]
display_name = "Hermes ACP"
```

安装后台服务:

```bash
dirextalk-connect daemon install --config /path/to/config.toml --force
```

同一台电脑连接多个 Dirextalk 节点时，每个后台服务使用不同的 service name：

```bash
dirextalk-connect daemon install --config /path/to/t1/config.toml --service-name t1.dirextalk.ai --force
dirextalk-connect daemon status --service-name t1.dirextalk.ai
```

## Dirextalk 约束

- Matrix 用户必须是本地 `@agent:<server>`，不能使用 portal owner session。
- `room_id` 为必填，且必须是真实持久化的 Dirextalk `agent_room_id`；bridge 会拒绝 `!agent:<domain>` 这类旧伪 id。
- 仅支持 `type = "matrix"`。
- 上游 cc-connect 的其他聊天平台已按 Dirextalk 需求删除。
