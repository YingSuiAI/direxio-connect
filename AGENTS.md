# AGENTS.md

This repository is the Dirextalk-maintained fork of cc-connect. It is a local bridge between one Dirextalk Matrix agents room and one local coding agent runtime.

## Project Scope

- The only supported chat platform is Dirextalk Matrix through `platform/matrix`.
- Do not add back Feishu, WPS Xiezuo, DingTalk, Telegram, Slack, Discord, LINE, WeCom, Weibo, Weixin, QQ, QQ Bot, or other chat-platform adapters.
- Keep support for local coding agent backends broad and neutral. Do not make Codex the only first-class backend; Codex, Claude Code, Gemini, Cursor, Copilot, Qoder, OpenCode, and similar local agent runtimes should be treated evenly where the architecture already supports them.
- The production binary name is `dirextalk-connect`.
- The npm package name is `dirextalk-connect`.
- The GitHub repository and release source is `https://github.com/YingSuiAI/dirextalk-connect`.

## Dirextalk Matrix Contract

- The bridge must use the real private Matrix room id persisted by Dirextalk Message Server as `agent_room_id`.
- Do not use legacy pseudo ids such as `!agent:<server>`.
- The Matrix account in the bridge config must be the local `@agent:<server>` identity returned by `agent.matrix_session.create`.
- The bridge must restrict sync and replies to the configured `room_id`.
- Replies to users are sent by `@agent:<server>`, not by the portal owner.
- User text and slash commands are ordinary Matrix text messages in the agent room. Do not add Dirextalk P2P action facades for normal chat text.
- Agent online display is Matrix-native room state. The bridge must publish `io.dirextalk.agent.status` with state key `@agent:<server>` and content `{"online":true}` when connected, then `{"online":false}` when stopped or disconnected.

## Config Rules

Generated Dirextalk configs should have this shape:

```toml
language = "auto"
data_dir = "<service-dir>/cc-connect/data"

[[projects]]
name = "<agent-node-id>"
admin_from = "@owner:<server>"

[projects.agent]
type = "<agent-backend>"

[projects.agent.options]
work_dir = "<workspace>"
cmd = "<optional explicit agent executable>"
mcp_url = "https://<server>/mcp"
mcp_server_name = "dirextalk-<server>"
mcp_agent_token = "<service agent token>"
mcp_node_id = "<agent-node-id>"

[[projects.platforms]]
type = "matrix"

[projects.platforms.options]
homeserver = "https://<server>"
access_token = "<agent matrix access token>"
user_id = "@agent:<server>"
room_id = "!<real-agent-room>:<server>"
share_session_in_channel = true
group_reply_all = true
auto_join = false
auto_verify = false
```

- `admin_from` is a project-level field under `[[projects]]`, not an agent option.
- `admin_from` must use full Matrix user IDs such as `@owner:a5.dirextalk.ai`. Matrix sender matching is exact and case-insensitive after trimming.
- If `admin_from` is empty, privileged commands such as `/dir`, `/shell`, `/show`, `/restart`, `/upgrade`, and `/diff` are blocked by default.
- Do not use `admin_from = "*"` in generated Dirextalk configs.
- Matrix `room_id` is required. Do not rely on `allowed_room_id` as a fallback, and do not use legacy pseudo ids such as `!agent:<server>`.
- `/dir reset` must restore the configured `work_dir` and clear the runtime directory override in `data_dir/projects/<project>.state.json`. In multi-workspace mode, clear only the matching workspace override.
- Runtime state under `data_dir` is not source code and should not be committed.

## Agent Backend Rules

- Preserve explicit command configuration. If `[projects.agent.options].cmd` and extra args are configured, the backend must use them instead of hardcoding a binary name.
- Keep app-server and stdio paths platform-neutral. Windows users must be able to run `dirextalk-connect.exe` from PowerShell without WSL-only assumptions.
- Agent backend fixes should include focused tests in the owning backend package, for example `go test ./agent/<backend> -count=1` for the changed backend.
- Do not silently drop streaming, card, Markdown, permission, or usage-reporting capabilities when adapting an agent backend.
- Before development and before any build or release, confirm the change keeps agent backend support neutral. Do not use `AGENTS=<single-agent>` for generic builds, release assets, npm packaging, or docs unless the user explicitly asks for a narrow debug binary.

## Packaging And Release

- Version bumps must keep these files in sync: `Makefile`, `npm/package.json`, README/INSTALL references, and release asset names.
- Generic release assets must include all supported local coding agent backends. `make build PLATFORMS_INCLUDE=matrix` and `make release-all` are the normal paths; `AGENTS=<name>` is only for explicit narrow debug builds.
- Release assets must use the `dirextalk-connect` name and the `YingSuiAI/dirextalk-connect` repository.
- The npm installer must download from GitHub Releases and should tolerate transient network failures with retries.
- Every npm publish must have the matching git tag, GitHub release, and release
  assets first. Do not publish `dirextalk-connect@X.Y.Z` while GitHub latest or
  `vX.Y.Z` is missing; the npm postinstall downloads from that release and will
  fail without it. The npm `prepublishOnly` check enforces this.
- Prefer `bash scripts/release.sh` for releases. It builds release assets,
  creates the matching `vX.Y.Z` tag/release when missing, runs the npm
  prepublish release-asset check, publishes npm, and verifies a real install.
- Before claiming npm install works, verify a real install of the just-published package, for example:

```powershell
npm install --prefix <temp-dir> dirextalk-connect@<version>
<temp-dir>\node_modules\.bin\dirextalk-connect.cmd --version
```

- Use `gh` for GitHub releases when available. A typical release verification path is:

```bash
go test ./tests/release_local/release_build_contract -count=1
make build PLATFORMS_INCLUDE=matrix
node --check npm/install.js
npm pack --dry-run --prefix npm
gh release view v<version> --repo YingSuiAI/dirextalk-connect
npm view dirextalk-connect@<version> version
```

## Development Workflow

- Work on the `cc-connect` branch unless the user asks for another branch.
- Use the shell native to the current environment. PowerShell is acceptable on Windows; Bash is acceptable on Linux, macOS, or WSL. Do not force WSL-only commands for Windows-local behavior.
- Prefer `rg` for search.
- Use `apply_patch` for manual source and documentation edits.
- Do not revert unrelated user changes. If runtime files or build artifacts appear, ignore them or add a targeted `.gitignore` entry when appropriate.
- Keep generated config paths in the format expected by the process that reads them. Windows-local `dirextalk-connect.exe` needs Windows-compatible paths, not `/mnt/c/...`.

## Verification

Choose validation based on the changed surface:

```bash
go test ./config ./core ./platform/matrix -count=1
go test ./agent/<changed-backend> -count=1
go test ./cmd/cc-connect -count=1
go test ./tests/release_local/release_build_contract -count=1
make build PLATFORMS_INCLUDE=matrix
node --check npm/install.js
npm pack --dry-run --prefix npm
git diff --check
```

- Run narrower tests first when diagnosing a bug, then broaden to the affected packages.
- For config behavior, include tests that parse or generate TOML rather than checking only string snippets.
- For Matrix behavior, verify sender filtering, room restriction, old-message deduplication, Markdown rendering, edits, typing, and reconnect behavior where relevant.
- For `/dir` behavior, verify both the in-memory agent work directory and the persisted project state file.

## Documentation Rules

- Keep README and INSTALL focused on Dirextalk operation, not the removed upstream multi-platform product.
- Do not document unsupported chat platforms.
- When changing public config, install, release, or command behavior, update README/INSTALL/config examples and this file together.
- Keep the package spelling `dirextalk-connect` unless the package is intentionally renamed across npm, docs, release tooling, and deployer integration.
