# Dirextalk Connect

This Dirextalk-maintained `cc-connect` fork bridges one real Dirextalk Matrix Agent room to one local coding-agent runtime.

## Scope

- Dirextalk Matrix under `platform/matrix` is the only chat platform. Keep local agent backends broad and neutral; generic builds and releases must not become single-provider binaries.
- The production binary/package is `dirextalk-connect`, released from `YingSuiAI/dirextalk-connect`.
- `config.example.toml`, parser/generator tests, and backend capability tests are the config contract. Do not duplicate a full generated config in `AGENTS.md`.

## Matrix Contract

- Use the persisted real `agent_room_id` and the local `@agent:<server>` Matrix session returned by `agent.matrix_session.create`.
- Restrict sync and replies to the configured room. User text and slash commands are ordinary Matrix text; replies are sent by `@agent`, not the portal owner.
- Publish `io.dirextalk.agent.status` state keyed by `@agent:<server>` with `online=true|false` as bridge connectivity changes.
- Privileged commands fail closed when the exact owner Matrix ID is not configured. Never use wildcard administration.

## Runtime And MCP

- Preserve explicit backend command/options. Runtime detection must not reinterpret an unknown backend as Codex or another semantic default.
- Remote MCP capability is explicit: session, project, host-managed, conditional, or unsupported. Unknown/incomplete support fails with an actionable result; do not write a generic JSON/env fallback.
- Prefer official session/process injection. Shared config writes preserve unrelated entries, serialize access, and protect persisted bearer credentials.
- A host-owned MCP registry remains authoritative when it launches a child agent; a child/backend override cannot bypass that registry.
- MCP transport and the agent process transport (ACP, app-server stdio, HTTP, tmux) are different contracts.
- Keep Windows, macOS, Linux, and WSL paths/process behavior separate. Config paths must match the local consuming process.

## Verification And Release

Use the smallest relevant checks, then broaden as needed:

```text
go test ./config ./core ./platform/matrix ./cmd/cc-connect -count=1
go test ./agent/<changed-backend> -count=1
go test ./tests/release_local/release_build_contract -count=1
make build PLATFORMS_INCLUDE=matrix
node --check npm/install.js
npm pack ./npm --dry-run
git diff --check
```

Config work needs parse/generate tests; Matrix work needs room/sender restriction plus reconnect/edit/stream coverage; backend work needs its capability/session tests. Release version, source tag, GitHub assets, npm metadata, generic backend set, and a real temporary install must agree. Publishing or mutating external release state requires explicit authorization.

Keep runtime state/build artifacts untracked, preserve unrelated changes, and commit only the current task.
