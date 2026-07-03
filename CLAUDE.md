# dirextalk-connect Development Guide

This fork is Dirextalk-only. It keeps the local coding-agent runtime and Matrix transport from cc-connect, but third-party chat platform integrations have been removed.

Use `AGENTS.md` as the current development contract. The important invariants are:

- only `platform/matrix` is supported;
- config validation rejects non-Matrix project platforms;
- `room_id` must point to the real Dirextalk agents room;
- packaging uses `dirextalk-connect` and the `dirextalk-connect` binary;
- release and update URLs point at `YingSuiAI/dirextalk-connect`.
