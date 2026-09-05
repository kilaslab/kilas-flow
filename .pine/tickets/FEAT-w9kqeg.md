---
id: FEAT-w9kqeg
title: Widen the n8n reference checkout and vendor the WAHA OpenAPI spec
status: done
priority: high
labels:
    - reference
    - guardrails
parent: EPIC-m42s3g
phase: p0
created: "2026-09-05T05:01:11Z"
updated: "2026-09-05T05:01:11Z"
---

## Scope

Every V2 phase reads n8n as a specification, never as a dependency. The reference checkout that reading happens against is `/Users/izzadev/projects/mitrachat/n8n`: a grafted `--depth 1 --filter=blob:none` clone of n8n 2.34.0 at commit `40dfa42ced26ba6fe01e511ec685f01ea77c0a83`, with a cone-mode sparse checkout whose 11 patterns materialise 911 of the 26,341 tracked files. What V2 needs is mostly not in those 911. `packages/@n8n/nodes-langchain` (889 files) is the specification p5 maps onto native Go. `packages/core/src/nodes-loader` (18 files) and `packages/cli/src/modules/community-packages` (25 files) are how n8n loads and validates a node package, which p2-7's source-tagged registry has to answer to. `packages/@n8n/node-cli` (164 files) and `packages/@n8n/eslint-plugin-community-nodes` (144 files) carry the conventions a generated node pack has to obey if imported workflows are to match it. Blobs fetch on demand — `git show HEAD:packages/core/package.json` already returns `n8n-core 2.34.0` from an unmaterialised path — so this is a widening in place of roughly 6.6 MB, not a re-clone.

Two of the existing 11 patterns are dead. `packages/frontend/editor-ui/src/components/ParameterInput.vue` and `…/ParameterInputList.vue` were written as if sparse patterns were globs; in cone mode a pattern is a directory, so both match nothing. The plan's proposed fix — add `packages/frontend/editor-ui/src/components` — is wrong and must not be carried out: that directory does not exist at 2.34.0 (`git ls-tree -r HEAD -- packages/frontend/editor-ui/src/components` returns zero entries). The parameter components live at `packages/frontend/editor-ui/src/features/ndv/parameters/components/`, and they are already on disk, materialised by the existing `packages/frontend/editor-ui/src/features/ndv` pattern. The correct action is to delete the two dead patterns, not to add a third.

The `.ee` exclusion is a licence line, not tidiness. n8n's root `LICENSE.md` states that source files containing `.ee.` in the filename or `.ee` in a directory name are **not** licensed under the Sustainable Use License at all and require a valid n8n Enterprise License as defined in `LICENSE_EE.md`. 1,117 tracked paths carry `.ee`, 455 of them under `packages/@n8n/ai-workflow-builder.ee`. None of the five widening targets contains a single `.ee` path, so the exclusion holds by simply never adding that package — but it must be asserted afterwards rather than assumed.

Separately, this ticket vendors the WAHA OpenAPI documents. `github.com/devlikeapro/n8n-nodes-waha` is MIT (`package.json` declares `"license": "MIT"`, `LICENSE.md` is the MIT text) at version `2025.2.9`, and holds `nodes/WAHA/v202409/openapi.json` (256 KB) and `nodes/WAHA/v202502/openapi.json` (290 KB) alongside the `WAHAOperationParser` / `WAHAResourceParser` / `WAHAOperationsCollector` helpers that turn them into node metadata. These two files are the exact inputs the published node pack was generated from, which is why p3-2's generator must read *these bytes* and not a spec pulled fresh from a live WAHA server: the resource and operation names that appear in real customer workflow JSON are a pure function of them. MIT permits vendoring with attribution, so unlike everything n8n-licensed, these belong in the repository.

## Acceptance criteria

- [x] `packages/@n8n/nodes-langchain`, `packages/core/src/nodes-loader`, `packages/cli/src/modules/community-packages`, `packages/@n8n/node-cli` and `packages/@n8n/eslint-plugin-community-nodes` are all materialised on disk, each verified by opening a named file inside it (for example `packages/core/src/nodes-loader/directory-loader.ts`).
- [x] The two dead `ParameterInput*.vue` patterns are removed, and every remaining entry in `git sparse-checkout list` resolves to at least one file on disk.
- [x] `packages/frontend/editor-ui/src/components` is not added; the NDV parameter components are reached at `packages/frontend/editor-ui/src/features/ndv/parameters/components/`.
- [x] No path containing `.ee` exists in the working tree after widening, and `packages/@n8n/ai-workflow-builder.ee` is absent.
- [x] The widening happened in place: `git -C …/n8n log -1` still reports the grafted commit `40dfa42`, and `git config remote.origin.partialclonefilter` still reports `blob:none`.
- [x] `devlikeapro/n8n-nodes-waha` is cloned outside `/Users/izzadev/projects/k-flow`, and both `openapi.json` documents are committed into this repository together with the upstream MIT licence text and a provenance note naming the repo, the commit and the package version they came from.
- [x] The only files this ticket adds to `/Users/izzadev/projects/k-flow` are the two WAHA specs, their licence and provenance note, and this ticket — `git status` proves no n8n-licensed byte entered the repository.
- [x] The reference-checkout location, its read-only status and the rule that it is never a build input are written into `.pine/memory/` where an agent will find them, not left in a chat transcript.

## Implementation Plan

Do the widening first, in one `git -C /Users/izzadev/projects/mitrachat/n8n sparse-checkout add` call listing the five directories, then a second `sparse-checkout set` (or `list` plus a hand-edit of `.git/info/sparse-checkout`) that drops the two `ParameterInput*.vue` entries while preserving the other nine. Order matters only in that removing patterns re-materialises the tree, so verify after the removal, not before it.

The trap that will bite is cone mode. `git sparse-checkout add` accepts a file path without complaining and silently matches nothing, which is exactly how the two dead patterns got in. After every change, walk the list and assert each pattern names a directory that now has files: `git sparse-checkout list | while read p; do printf '%s %s\n' "$(find "$p" -type f 2>/dev/null | wc -l)" "$p"; done`. A zero is a broken pattern, not an empty directory. The second trap is the network: `blob:none` means each newly materialised file is a lazy fetch, so widening on a bad connection appears to hang rather than fail; do it once, deliberately, and confirm the ~6.6 MB landed before assuming anything is missing. The third is that adding a directory takes its entire subtree — verified today as `.ee`-free for all five targets, but assert it again afterwards with `find . -path ./.git -prune -o -name '*.ee*' -print`, because a later n8n version will not necessarily stay clean.

For the WAHA clone, a shallow clone of `master` is enough (`git clone --depth 1 https://github.com/devlikeapro/n8n-nodes-waha.git`) and it must land beside the n8n checkout under `/Users/izzadev/projects/mitrachat/`, never inside this repository. Pin the commit you cloned in the provenance note; `master` moves, and p3-2's generated names must be reproducible against a fixed spec.

One decision remains open: where the vendored specs live. The candidates are a new top-level `third_party/waha/`, the existing `schemas/` directory (which today holds only KilasFlow's own `workflow-v1.schema.json`), or a `testdata` directory under whatever package p3-2 puts the generator in. Recommend `third_party/waha/` holding `openapi-202409.json`, `openapi-202502.json`, `LICENSE` (the upstream MIT text verbatim) and `PROVENANCE.md`. It reads as third-party to anyone auditing the tree, it keeps 546 KB of foreign JSON out of the Go package tree where a future `go:embed` directory pattern could sweep it up by accident, and it leaves `schemas/` meaning "contracts KilasFlow owns". Whatever is chosen, the licence file must sit next to the JSON rather than being referenced from elsewhere — a vendored MIT file with its notice one directory away is a licence violation waiting to be found.

Do not vendor `waha.svg` in this ticket even though it is under the same MIT licence; node icons are p2-5's problem and it should decide the format and the serving path.

## References

- Roadmap plan, p0 section, entry V2-p0-1: `.pine/roadmap.md`.
- Reference checkout: `/Users/izzadev/projects/mitrachat/n8n` at `40dfa42ced26ba6fe01e511ec685f01ea77c0a83` (n8n 2.34.0), `LICENSE.md` and `LICENSE_EE.md`.
- `https://github.com/devlikeapro/n8n-nodes-waha` — `package.json` (`@devlikeapro/n8n-nodes-waha` 2025.2.9, MIT), `LICENSE.md`, `nodes/WAHA/v202409/openapi.json`, `nodes/WAHA/v202502/openapi.json`.
- Git sparse-checkout cone-mode semantics: `git help sparse-checkout`, "CONE PATTERN SET".

## Outcome

Widened in place: 13 cone patterns, 911 → 2102 materialised files at
`40dfa42` (n8n 2.34.0), `remote.origin.partialclonefilter` still `blob:none`.

All five targets landed and were probed by a named file inside each:
`packages/@n8n/nodes-langchain` (889), `packages/@n8n/node-cli` (164),
`packages/@n8n/eslint-plugin-community-nodes` (144),
`packages/cli/src/modules/community-packages` (25),
`packages/core/src/nodes-loader` (18, probed at `directory-loader.ts`).

**Three** dead patterns were removed, not two. The ticket named
`ParameterInput.vue` and `ParameterInputList.vue`; widening exposed a third,
`packages/frontend/editor-ui/src/app/components/canvas`, which resolves to zero
files in the tree at 2.34.0. It got the same treatment for the same reason: the
canvas lives at `packages/frontend/editor-ui/src/features/workflows/canvas` and
is already materialised by the existing `features/workflows` pattern, so the fix
is deletion, not a replacement pattern. Every one of the 13 surviving patterns
now resolves to at least one file on disk.

`packages/frontend/editor-ui/src/components` was not added — the NDV parameter
components are reached at `features/ndv/parameters/components/`, confirmed
present. No path containing `.ee` exists in the working tree and
`packages/@n8n/ai-workflow-builder.ee` is absent.

WAHA was cloned to `/Users/izzadev/projects/mitrachat/n8n-nodes-waha` at
`b06e8f57ce8da91ed684841d14a3532f6d134712` (`@devlikeapro/n8n-nodes-waha`
2025.2.9, MIT). Both OpenAPI documents are vendored byte-for-byte under
`third_party/waha/` with the upstream MIT text as `LICENSE` beside them and a
`PROVENANCE.md` recording repo, commit, version, upstream paths and per-file
SHA-256. The two files differ in indentation upstream (202409 spaces, 202502
tabs); that is preserved, since normalising it would break the digests and the
generated operation names. 202409 declares 98 operations, 202502 declares 124 —
the latter matching the operation count the epic attributes to the published
pack.

The only files this ticket adds to the repository are the two specs, their
licence, the provenance note and memory updates. No n8n-licensed byte entered
the tree.

Reference-checkout rules, the cone-mode trap and the vendoring rule were written
to `.pine/memory/n8n-reference.md` via `pine learn`.
