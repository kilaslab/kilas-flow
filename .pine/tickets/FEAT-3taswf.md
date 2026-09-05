---
id: FEAT-3taswf
title: Publish @kilasflow/sdk to npm
status: todo
priority: high
labels:
    - sdk
    - release
deps:
    - FEAT-yx0qt6
    - FEAT-53fht8
parent: EPIC-m42s3g
phase: p10
created: "2026-09-05T11:51:49Z"
updated: "2026-09-05T11:51:49Z"
---

## Scope

`@kilasflow/sdk` is a finished package that no one can install. It has three export subpaths (`.`, `./server`, `./browser`) with `types` and `import` conditions on each, a `files` array naming `dist` and `README.md`, a build (`tsc -p tsconfig.build.json`), a typecheck, a vitest suite, and a runnable example under `examples/host-page`. It is not marked `private`. Everything about the manifest says "intended for publication".

Nothing publishes it, and several things would have to be true first.

`sdk/dist/` is gitignored, so the only artifact the `files` array names does not exist in a clone. The root `Makefile` has no SDK target of any kind — no build, no test, no typecheck, no generate — so the package is built only by somebody who already knows to run `pnpm --dir sdk build` by hand. A host wanting to use it today must vendor the source and build it themselves, which is why every integration is currently a copy.

The manifest is also incomplete in ways npm surfaces to anyone looking at the package page. There is no `repository`, no `homepage`, no `bugs`, and no `keywords`, so a published package would carry no link back to the project it belongs to. There is no `publishConfig`. And `"license": "MIT"` contradicts the repository's Apache-2.0 `LICENSE` — a discrepancy V2-p10-4 resolves, and one that becomes an irreversible published claim the first time this ticket runs.

The version is the other unresolved half. `sdk/src/version.ts` hardcodes `SDK_VERSION = '0.1.0'` and `package.json` says `0.1.0`; two literals that must agree, with nothing checking that they do. `sdk/README.md` documents a policy — semver for the package surface, `API_VERSION` for the path — that nothing enforces, and no release has ever exercised it.

The example is worth fixing at the same time, because it is the first thing an evaluator runs. `sdk/examples/host-page/server.mjs` is a complete demonstration — backend mints a session, page mounts the editor, page receives execution events — and it necessarily runs against a KilasFlow the reader has built from source. Once V2-p10-2 publishes an image, the example can run against a pulled container instead, which turns a half-hour evaluation into a two-command one.

## Acceptance criteria

- [ ] `npm install @kilasflow/sdk` in an empty project resolves, and importing all three subpaths type-checks under `moduleResolution: bundler` and under `node16`.
- [ ] The published tarball contains `dist` and `README.md` and nothing else — no source, no tests, no `.tmp`, no generated intermediate.
- [ ] `package.json` and `src/version.ts` cannot disagree: a check fails the build when `SDK_VERSION` and the manifest version differ.
- [ ] The manifest declares `repository`, `homepage`, `bugs` and a licence that matches the repository's, per the decision recorded in V2-p10-4.
- [ ] Publishing is driven by a git tag through the V2-p10-1 pipeline, with npm provenance attested, and no publish is possible from a working tree that is not that tag.
- [ ] A `CHANGELOG.md` records each release and states, per entry, whether it is additive, a fix, or breaking, in the vocabulary V2-p10-4 defines.
- [ ] The Makefile gains SDK targets so building, testing, typechecking and regenerating the package are the same commands on a laptop and in the pipeline.
- [ ] `examples/host-page` runs against a published container image and a published package with no checkout of this repository, and its README says so.

## Implementation Plan

Sequence this after V2-p10-2, not merely alongside it. The example's value depends on a pullable image, and the version-stamping story is the same story in both tickets: a published npm package that says `0.1.0` while the server it targets says `21056a1-dirty` teaches a consumer that neither number means anything.

Keep `tsc` as the build. There is no bundler and there does not need to be one — the package is ESM-only, has no runtime dependencies, and ships declarations. Adding a bundler here would be work with no beneficiary.

Two mechanical traps in publishing an ESM-only package with export conditions. First, `moduleResolution: node16` in a consumer resolves the `types` condition per subpath, so all three subpaths need their declarations emitted and present; a `dist` that builds `index.d.ts` but not `server.d.ts` fails only for consumers on that setting, which is not the setting the author will be testing on. Verify by installing the packed tarball into a scratch project under both resolutions rather than by trusting the build. Second, `files: ["dist", "README.md"]` silently excludes the licence file; npm includes `LICENSE` automatically when it is at the package root, and this package root is `sdk/`, which has none.

For provenance, npm requires the publish to run from a public CI workflow with an OIDC token. That is free once V2-p10-1 exists and is very awkward to add later, so do it in the same change.

The version-agreement check is three lines and prevents a class of bug that is otherwise only found by a consumer: read `package.json`, read `SDK_VERSION`, compare, fail. Put it in the test suite so it runs without ceremony.

One decision to settle rather than default into: whether the first published version is `0.1.0` or `0.x` continues until the API contract from V2-p10-4 is considered stable. Recommend staying on `0.x` until then and saying so in the README, because semver's pre-1.0 allowance is the honest description of a package whose server surface is still being reshaped by p1 through p9 — and claiming `1.0.0` before `FEAT-ddzk2k` lands would promise a stable authentication story that does not exist.

## References

- Roadmap plan, p10 section, entry V2-p10-8: `.pine/roadmap.md`.
- `sdk/package.json` — the three export subpaths, `files`, the missing `repository`/`homepage`/`bugs`, and the MIT declaration.
- `sdk/src/version.ts` — `SDK_VERSION`, the literal that must agree with the manifest.
- `sdk/README.md` — the `## Versioning` section and the entry-point table.
- `sdk/tsconfig.build.json` and the `build` script — the whole build.
- `.gitignore` — the rule that keeps `sdk/dist/` out of the repository.
- `Makefile` — where the missing SDK targets belong, beside the existing web targets.
- `sdk/examples/host-page/server.mjs` and its README — the example to repoint at a published image.
- `.pine/tickets/FEAT-bscygc.md` — V2-p10-4, which resolves the licence and defines the release vocabulary this ticket uses.
