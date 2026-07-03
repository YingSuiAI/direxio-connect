# Contributing

`dirextalk-connect` is maintained for Dirextalk deployments only.

Before opening a change, check that it preserves these boundaries:

- no non-Matrix chat platform integrations;
- no public Matrix account or portal-owner session in examples;
- no legacy `!agent:<domain>` pseudo room ids;
- npm package remains `dirextalk-connect`;
- binary and operator docs use `dirextalk-connect`;
- repository links point to `https://github.com/YingSuiAI/dirextalk-connect`.

Run focused tests for the touched packages and `make build` before submitting changes.
