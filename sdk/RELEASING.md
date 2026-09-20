# Releasing `@kilasflow/sdk`

This file is for whoever cuts the release. It is **not** shipped in the package —
`sdk/package.json`'s `files` is an allowlist (`dist`, `README.md`,
`CHANGELOG.md`, `LICENSE`), so the tarball carries none of it.

## 1. What a release is

Pushing a tag is the only way anything is published.

| Tag | What it publishes |
| --- | --- |
| `sdk-vX.Y.Z` | this package, from the `sdk` job of `.github/workflows/release.yml` |
| `vX.Y.Z` | the container image, from the `image` job of the same workflow |

Nothing else runs a publish: no branch push, no `workflow_dispatch`, no laptop.
A prerelease tag such as `sdk-v0.2.0-rc.1` publishes under the `next` dist-tag
instead of `latest`; the workflow derives that from the version, not from a
hand-written flag.

The `prepublishOnly` hook (`node scripts/release.mjs guard`) refuses a publish
whose context is not a push of the matching `sdk-v` tag. **It is a foot-gun
guard, not a wall.** `npm publish <tarball>` runs no lifecycle scripts at all, so
nothing stops a determined laptop publish but the registry settings in section 2.
The real controls are the trusted publisher plus "disallow tokens", and a tag
ruleset — both owner-only.

## 2. One-time owner setup

Do these in order. Steps (c) and (d) exist because of an ordering problem that
the npm CLI states plainly and nothing can work around.

**(a) The npm scope.** `@kilasflow` requires an npm org or user named
`kilasflow` that you own (npmjs.com/org/create). `npm view @kilasflow/sdk`
answers `E404` today, but whether the scope itself is taken cannot be checked
without a login — confirm ownership before the first tag.

**(b) Two-factor authentication on the account.**

**(c) The chicken and egg: the first publish needs a token.** A trusted
publisher can only be configured for a package that **already exists** —
`npm help trust` lists "Package must exist" as a prerequisite (npm 11.10 or
later). So the first `sdk-v0.1.0` tag cannot authenticate by OIDC alone. To
bootstrap:

1. Create a **granular** npm token with read-write on the `@kilasflow` scope. It
   must be scope-scoped, not package-scoped, because the package does not exist
   yet and a package-scoped token cannot be created before it does.
2. Enable "bypass two-factor authentication" on it — CI cannot answer an OTP
   prompt.
3. Give it the shortest workable expiry: npm caps write tokens at 90 days.
4. Store it as the repository secret `NPM_TOKEN` and push the tag.

Provenance is still attested on this first release: it comes from GitHub's OIDC
identity and Sigstore, not from the npm login. `npm publish --provenance` in the
workflow, and the `id-token: write` permission, are unaffected by which registry
credential authenticated the upload.

The laptop alternative is **not recommended** — it puts an extra, un-attested
version on the registry permanently — but if you take it, build first and bypass
both the hook and `publishConfig.provenance`:

```sh
cd sdk
pnpm install --frozen-lockfile
npm run build
npm publish --ignore-scripts --provenance=false --access public
```

`--ignore-scripts` is what skips the guard (the hook refuses a laptop), and
`--provenance=false` is required because `publishConfig.provenance` is true.

**(d) Configure the trusted publisher** (after the first publish). Use the npm
web UI, or:

```sh
npm trust github @kilasflow/sdk --file release.yml --repo kilaslab/kilas-flow
```

Owner `kilaslab`, repo `kilas-flow`, workflow filename `release.yml`, environment
blank. Run it as an interactive owner with 2FA: per `npm help trust`, `npm trust`
rejects bypass-2FA tokens, so the bootstrap token from (c) cannot do it. Only one
trust configuration per package is supported.

**(e) Lock it down.** Set the package's publishing access to "Require two-factor
authentication and disallow tokens", delete the `NPM_TOKEN` repository secret,
and delete the `BOOTSTRAP ONLY` env and `if` block in the workflow's publish step.
From then on the OIDC token is the only credential the release has.

**(f) Protect the tags.** Add a GitHub tag ruleset limiting creation of `sdk-v*`
and `v*` to maintainers. Anyone who can push a matching tag can start a publish,
and the workflow's only other guard is that the tag, the manifest, `SDK_VERSION`
and the changelog agree — which is a consistency check, not an authorization.

## 3. Every release

1. Bump all three version literals together:
   - `sdk/package.json` → `version`
   - `sdk/src/version.ts` → `SDK_VERSION`
   - `sdk/examples/host-page/package.json` → the `@kilasflow/sdk` dependency

   `make sdk-version-check` and `sdk/test/version.test.mjs` fail if the first two
   disagree; `sdk/test/release.test.mjs` fails if the example pin drifts.

2. Add a `## X.Y.Z` entry to `sdk/CHANGELOG.md`, every bullet labelled
   **Additive**, **Fix** or **Breaking** (see the heading in that file).

3. Run the release gate and read the tarball list it prints — every byte of it is
   immutable once published:

   ```sh
   make sdk-release-check
   ```

   It runs, in order: `sdk-check`, `sdk-test`, `sdk-build`, `sdk-version-check`,
   `generate-types-check`, `sdk-package-check`, `sdk-example-check`. `sdk/scripts/release.mjs facts`
   runs the same non-registry rules, and the workflow asserts the two lists match.

4. Commit, and make sure **main CI is green**. The release job re-runs the SDK
   gates but not e2e, so a red main can still produce a published package.

5. Tag and push that one tag by name:

   ```sh
   git switch main && git pull --ff-only
   git tag -a sdk-vX.Y.Z -m "@kilasflow/sdk X.Y.Z"
   git push origin sdk-vX.Y.Z
   ```

   Never `git push --tags`. A published version is immutable: never move a tag;
   fix forward with a patch release and `npm deprecate` the bad version.

## 4. Post-publish verification

```sh
npm view @kilasflow/sdk@X.Y.Z version dist-tags
npm view @kilasflow/sdk@X.Y.Z dist.attestations
make sdk-verify-published                 # registry install + the three-resolution typecheck
make sdk-verify-published SDK_SPEC=@kilasflow/sdk@X.Y.Z   # a different version
```

`make sdk-verify-published` installs from the registry into a scratch project
outside the repository, typechecks under `bundler`, `node16` and `nodenext`, and
imports all three subpaths. That is everything `sdk-package-check` runs except
the tarball file list: that assertion reads the tarball `npm pack` builds in this
tree, and this path installs the published package instead, so the contents of
the published tarball are not asserted here.

Then, in an empty directory:

```sh
npm init -y
npm install @kilasflow/sdk
npm audit signatures
```

Finally run `sdk/examples/host-page` against a published image per its README.

## 5. After the FIRST publish only

These statements become false the moment `0.1.0` is on the registry. Flip them
in the same change that follows the first release (line numbers as of the first
release):

- `CHANGELOG.md:10` (root) — the `[Unreleased]` / release note saying
  `@kilasflow/sdk` is not on npm.
- `SECURITY.md:6` — the same claim.
- `docs/src/content/docs/guides/embedding.md:146-149` — "The SDK is not on npm
  yet".
- `sdk/examples/reference-host/README.md:16` and `:74` — the install-from-checkout
  instructions; its `package.json` `file:../..` dependency switches to a version
  range (the README already says to do this).
- `sdk/examples/host-page/README.md` — the "Before the first release is
  published" block.
- `docs/src/content/docs/guides/community-nodes.md:14` — "not published yet".
  This page is owned by BUG-vzzkg3; list it in the release PR rather than editing
  it from here.
- `pkg/sdk/doc.go:16` — already says "on npm", ahead of reality; it becomes true
  and needs no change.

## 6. Optional hardening (owner decisions)

Not enabled by default:

- A protected GitHub environment with a required reviewer on the publish job.
- Splitting verify and publish into two jobs.
- A check that the tagged commit is an ancestor of `main` (needs
  `fetch-depth: 0`), so a tag cannot be cut from an unreviewed branch.
