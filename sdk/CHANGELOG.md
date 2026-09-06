# Changelog

Every entry states whether it is **additive** (new surface, existing callers
unaffected), a **fix** (behavior brought in line with what was documented), or
**breaking** (an existing caller must change). The package stays on `0.x`
until the server surface it targets is considered stable; see `README.md`.

## 0.1.0

First published release.

- **Additive**: the full operation surface — workflows and versions, executions,
  credentials, API keys, schedules, node catalogue, interop, embed sessions,
  system — across `@kilasflow/sdk`, `@kilasflow/sdk/server` and
  `@kilasflow/sdk/browser`.
- **Additive**: `apiKey` on `TransportOptions`, a convenience sending
  `Authorization: Bearer <key>` beside the existing `headers` escape hatch,
  which remains supported and wins on conflict.
- **Additive**: `tenantClientFactory` for one fixed-credential client per
  tenant, and a single-use stream-ticket handshake in
  `subscribeExecutionEvents` with mint-per-connect reconnect and resume.
- **Fix**: the manifest licence is Apache-2.0, matching the repository root
  `LICENSE`.
