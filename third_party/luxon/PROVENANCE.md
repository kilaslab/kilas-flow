# Luxon: provenance

The embedded JavaScript runtime (`internal/jsrun`, EPIC-tjnr1z) runs this bundle so that n8n Code nodes get real Luxon. Code nodes use it through `DateTime`, `Duration`, `Interval`, `$now` and `$today`.

## Upstream

| | |
| --- | --- |
| Repository | <https://github.com/moment/luxon> |
| Package | `luxon` (npm) |
| Version | `3.7.2` |
| Tarball | <https://registry.npmjs.org/luxon/-/luxon-3.7.2.tgz> |
| Tarball integrity | `sha512-vtEhXh/gNjI9Yg1u4jX/0YVPMvxzHuGgCm6tC5kZyb08yjGWGnqAjGJvcXbqQR2P3MyMEFnRbpcdFS6PBcLqew==` (matches the npm registry) |
| Licence | MIT. See `LICENSE` in this directory |
| Retrieved | 2026-09-23 |

## Files

| Vendored path | Upstream path | SHA-256 |
| --- | --- | --- |
| `luxon.min.js` | `build/global/luxon.min.js` | `516c5fea5c82bdf1fb5c472b9aafdca9cda6dc806b2c12ce9d62c2e0fc348104` |
| `LICENSE` | `LICENSE.md` | `6cb2f2bf697ee9c6fa9eb8f227c63ee6e7a3cba42d4717f14c745ef9b6cbc006` |

- Both files are copied **byte for byte**. Do not reformat them, or the digests stop matching. `TestVendoredBundlesMatchTheirProvenanceDigests` in `internal/guardrails` checks them.
- `luxon.min.js` is the "global" build: running it defines a single global binding, `luxon`.
- `embed.go` is KilasFlow-authored. It only embeds the bundle so the runtime can compile it once per process, and it is not part of the upstream package.

## Licence position

MIT permits redistribution with attribution, which is what this directory provides. Nothing else from the upstream package is vendored: not the source maps, not the other builds, not the TypeScript sources.

Luxon is not an n8n package. n8n *documents* that its Code node offers Luxon, and KilasFlow provides the same library from its upstream author. No n8n code or bytes are involved.

See `.pine/memory/licensing.md` for the full licence boundary.
