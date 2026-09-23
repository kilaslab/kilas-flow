# lodash: provenance

The embedded JavaScript runtime (`internal/jsrun`, EPIC-tjnr1z) runs this bundle, so an n8n Code node can call `require('lodash')`.

## Upstream

| | |
| --- | --- |
| Repository | <https://github.com/lodash/lodash> |
| Package | `lodash` (npm) |
| Version | `4.18.1` |
| Tarball | <https://registry.npmjs.org/lodash/-/lodash-4.18.1.tgz> |
| Tarball integrity | `sha512-dMInicTPVE8d1e5otfwmmjlxkZoUpiVLwyeTdUsi/Caj/gfzzblBcCE5sRHV/AsjuCmxWrte2TNGSYuCeCq+0Q==` (matches the npm registry) |
| Licence | MIT. See `LICENSE` in this directory |
| Retrieved | 2026-09-23 |

## Files

| Vendored path | Upstream path | SHA-256 |
| --- | --- | --- |
| `lodash.min.js` | `lodash.min.js` | `a8d7e6291ad80256f976ace90824a71018d2f706992c9107b20bdced97bee27b` |
| `LICENSE` | `LICENSE` | `f71e8ed126b46346494aad5486874cd8f0aafe95092ed67d2e3cb6110f939abc` |

- Both files are copied **byte for byte**. `TestVendoredBundlesMatchTheirProvenanceDigests` in `internal/guardrails` checks the digests.
- The bundle keeps its own `@license` header.
- `embed.go` is KilasFlow-authored and not part of the upstream package.

## Licence position

MIT permits redistribution with attribution, which is what this directory provides. Only the minified full build is vendored; none of lodash's per-function modules are.

See `.pine/memory/licensing.md` for the full licence boundary.
