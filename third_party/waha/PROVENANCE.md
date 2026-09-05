# WAHA OpenAPI documents — provenance

These two OpenAPI documents are the **only third-party bytes vendored into this
repository**. They are the exact inputs the published WAHA node pack was
generated from, which is why the generator in this project must read *these*
files rather than a specification pulled fresh from a live WAHA server: the
resource and operation names that appear in real customer workflow JSON are a
pure function of these bytes.

## Upstream

| | |
| --- | --- |
| Repository | <https://github.com/devlikeapro/n8n-nodes-waha> |
| Package | `@devlikeapro/n8n-nodes-waha` |
| Version | `2025.2.9` |
| Commit | `b06e8f57ce8da91ed684841d14a3532f6d134712` (2025-08-25) |
| Licence | MIT — see `LICENSE` in this directory |
| Retrieved | 2026-09-05 |

## Files

| Vendored path | Upstream path | SHA-256 |
| --- | --- | --- |
| `openapi-202409.json` | `nodes/WAHA/v202409/openapi.json` | `ec931063faf1b209bb9111dee620c570a9f474d9c5520e98c2ede5241e3a2cd9` |
| `openapi-202502.json` | `nodes/WAHA/v202502/openapi.json` | `ec332a8c6421047bd14c4b810b31b1cd31b499911072d4e1621ffaca65cc1248` |
| `LICENSE` | `LICENSE.md` | `053cb0df9afcf71ac340bdddccb3c25b280a8645e64ba93a709ebc0fbe0f4e35` |

Both documents are copied **byte for byte**. `openapi-202409.json` is
space-indented and `openapi-202502.json` is tab-indented; that difference is
upstream's and must not be normalised, or the digests above stop matching.

`openapi-202409.json` declares 98 operations and `openapi-202502.json` declares
124 — the two published WAHA API versions the node pack targets.

## Licence position

The upstream `LICENSE.md` is the MIT licence text. Its copyright line reads
`Copyright 2022 n8n` because the package was started from n8n's MIT-licensed
community-node starter template; that line is part of the notice being
reproduced and is preserved verbatim, as MIT requires. It does **not** make
these files subject to n8n's Sustainable Use License — `package.json` declares
`"license": "MIT"` and the MIT grant is what applies.

MIT permits redistribution with attribution, which is what this directory is.
Nothing else from the upstream package is vendored: the TypeScript parsers, the
node icon and the generated node metadata all stay outside this repository.

## Not vendored

`github.com/devlikeapro/waha-n8n-templates`, the source of the official WAHA
template corpus, carries **no licence file** and the GitHub API reports
`license: null`. That is stricter than the Sustainable Use License, not looser.
Those templates are fetched into a gitignored directory when a corpus is needed
and are never committed here.

See `.pine/memory/licensing.md` for the full licence boundary.
