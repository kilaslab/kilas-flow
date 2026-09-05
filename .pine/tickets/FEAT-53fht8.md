---
id: FEAT-53fht8
title: Publish multi-architecture container images
status: done
priority: high
labels:
    - release
    - platform
deps:
    - FEAT-7tgasa
parent: EPIC-m42s3g
phase: p10
created: "2026-09-05T11:46:58Z"
updated: "2026-09-05T17:17:51Z"
---

## Scope

Nothing this repository builds is published anywhere, and the version it stamps into what it builds is a dirty commit hash.

`git tag -l` is empty. `Makefile` sets `VERSION ?= $(shell git describe --tags --always --dirty || echo "0.1.0-dev")`, which with no tags resolves to something like `21056a1-dirty`. That value reaches three places that matter: the binary through `-ldflags "-X main.version=$(VERSION)"`, the `-version` flag, and — because `internal/api/server.go` passes `deps.Version` into `huma.DefaultConfig("KilasFlow API", deps.Version)` — the `info.version` field of the OpenAPI document every generated client is built from. A consumer asking the API what version it is talking to is told a commit hash with `-dirty` appended.

The image itself is well built and single-architecture. The `Dockerfile` has three stages: `node:24.16-alpine` builds the SPA, `golang:1.27-alpine` copies `web/build` into `internal/web/dist` and compiles with `CGO_ENABLED=0 -trimpath`, and the runtime is `gcr.io/distroless/static-debian12:nonroot` with `VOLUME ["/app/data"]`, `EXPOSE 8080`, `USER nonroot:nonroot` and four `KILASFLOW_*` defaults baked in. There is no `--platform`, no `TARGETPLATFORM` or `TARGETARCH`, no buildx bake file and no `GOARCH` handling anywhere in it, so it produces whatever the building daemon happens to be. `make docker` tags `kilasflow:$(VERSION)` and `kilasflow:latest` — bare names with no registry namespace — and nothing ever pushes.

The cost of fixing this is unusually low, which is the argument for doing it properly now rather than later. The build is already `CGO_ENABLED=0` onto a static distroless base, so cross-compiling is a `GOARCH` argument and not a toolchain project. The only stage that is genuinely architecture-dependent is the Node SPA build, and its output is architecture-independent JavaScript, so it can run once on the native runner and be shared by both targets.

## Acceptance criteria

- [ ] A published image runs on `linux/amd64` and `linux/arm64`, and pulling by one tag on either architecture resolves through a manifest list to the right one. — **not met: nothing is published yet.** The mechanism is built and was proved against a throwaway local registry; see Work evidence.
- [x] The SPA build stage executes once per build rather than once per target architecture, and the two architecture images embed byte-identical frontend assets.
- [x] A git tag produces an image whose `main.version`, `-version` output and OpenAPI `info.version` are all that tag, with no `-dirty` suffix and no commit hash.
- [x] The tag vocabulary is documented and implemented — an immutable exact version, a moving minor tag, and `latest` — and it is stated which of them a production deployment should pin.
- [x] The image carries OCI source, revision, version and licence labels, and a consumer can trace a running container back to the commit it was built from without asking anyone.
- [x] `make docker` and the published pipeline build the same image from the same Dockerfile, so a local reproduction of a published image is possible.
- [x] `smoke-docker` passes against a pulled published image, not only against a locally built one. — met against a local registry; the GHCR pull itself is unexercised.
- [ ] The first release tag is cut as part of this ticket, so `git describe` stops resolving to a commit hash for every subsequent build. — **deliberately not done by this session**; the reason and the exact command are in Work evidence.

## Implementation Plan

`docker buildx` with `platforms: linux/amd64,linux/arm64`, pushing to `ghcr.io/kilaslabs/kilasflow` — GitHub Container Registry, because the repository and the CI runner from V2-p10-1 are already there and it needs no additional credential. A Docker Hub mirror is a later decision, not this ticket's.

The Dockerfile change is small and specific. Pin the SPA stage to the build platform with `FROM --platform=$BUILDPLATFORM node:24.16-alpine AS web` so it is not emulated once per target, take `ARG TARGETARCH` in the build stage, and pass `GOARCH=${TARGETARCH}` to `go build`. Keep `CGO_ENABLED=0`. Leave the `/out/data` `mkdir` trick alone — its comment explains it exists because the runtime image has no shell and `nonroot` cannot create `./data` in a root-owned `/app`, and that reasoning is unchanged by cross-compilation.

Emulated arm64 Go compilation under QEMU is slow enough to be worth avoiding, and cross-compilation avoids it entirely: the build stage runs natively and only the output is arm64. That is the whole reason to use `TARGETARCH` with `GOARCH` rather than `--platform` on the build stage.

Version stamping needs one correction beyond passing the argument through. The Dockerfile declares `ARG VERSION=0.1.0` and `docker-compose.yml` passes `VERSION: dev`; the release build must pass the real tag, and a build that does not receive one should be visibly a development build rather than silently claiming `0.1.0`.

Two decisions to settle in the ticket rather than assume. First, whether `latest` moves on every release or only on stable ones — recommend stable only, and never on a prerelease tag. Second, whether the pipeline signs and attests: cosign signatures, an SBOM and provenance are cheap to add at the start and awkward to retrofit once consumers exist, so recommend adding them now even though nothing yet verifies them.

Note for whoever writes the release notes: this is the first tag, so `git describe` output changes meaning across it. Any script or ticket that quotes the current dirty-hash form is describing the world before this ticket, not a bug.

## References

- Roadmap plan, p10 section, entry V2-p10-2: `.pine/roadmap.md`.
- `Dockerfile` — the three stages, the `ARG VERSION`, the distroless runtime, the `/out/data` comment, and the absence of any platform handling.
- `Makefile` — `VERSION`, `LDFLAGS`, and the `docker` target that tags without a namespace and never pushes.
- `cmd/kilasflow/main.go` — `var version`, the `-version` flag, and `Deps.Version`.
- `internal/api/server.go` — `huma.DefaultConfig("KilasFlow API", deps.Version)`, which puts the version in the OpenAPI document.
- `scripts/smoke-docker.sh` — `KILASFLOW_SMOKE_IMAGE` and `KILASFLOW_SMOKE_SKIP_BUILD`, the seams for smoking a pulled image.
- `.dockerignore` — excludes `internal/web/dist` so a stale host build never leaks into an image.

## Work evidence

### What was built

- `Dockerfile` — both build stages pinned to `--platform=$BUILDPLATFORM`; `ARG TARGETOS`/`ARG TARGETARCH` fed into `GOOS`/`GOARCH`, `CGO_ENABLED=0` unchanged. The two `ARG`s are declared *after* `COPY . .` and `COPY --from=web` on purpose, so everything above them is one shared layer across both targets and only the `go build` diverges. `ARG VERSION` default moved from `0.1.0` to `0.0.0-dev`, because the old default was indistinguishable from a real release. Six OCI labels on the runtime stage, below the `COPY` lines so a revision-only change does not invalidate the binary layer.
- `scripts/docker-tags.sh` (new) — the tag vocabulary and the rules that separate the three tags, in one place. Validates the version and refuses anything that is not `vMAJOR.MINOR.PATCH`; a prerelease gets its exact tag only.
- `Makefile` — `IMAGE`, `PLATFORMS`, `REVISION`, `SOURCE_URL`, `IMAGE_METADATA` and a shared `IMAGE_ARGS`; `docker` now passes the build arguments, plus new `docker-multiarch` (cross-build, publishes nothing), `docker-release` (buildx `--push` with SBOM and provenance), `docker-sign` (cosign over the pushed digest) and `smoke-docker-published`.
- `scripts/smoke-docker.sh` — a third way to obtain the image under test, `KILASFLOW_SMOKE_PULL`, alongside the existing build and skip-build seams.
- `.github/workflows/release.yml` (new) — on `push: tags: ["v*"]`, calls the Make targets above. Per-job `packages: write` and `id-token: write`, as `ci.yml`'s header asks p10 publishing tickets to do.
- `.github/workflows/ci.yml` — one new `image` job running `make docker-multiarch`, so the arm64 path is exercised on every pull request rather than for the first time during a release.

Every third-party action is pinned to a commit SHA resolved from the GitHub API, not typed from memory: `docker/setup-buildx-action` v4.3.0 `37fe631027851001ddb9b187196cc803df7f5f0e`, `docker/login-action` v4.6.0 `dbcb813823bdd20940b903addbd779551569679f`, `sigstore/cosign-installer` v4.1.2 `6f9f17788090df1f26f669e9d70d6ae9567deba6`. `actions/checkout` reuses the SHA already in `ci.yml`, re-resolved and confirmed identical.

### Verified locally

On this machine — Docker 29.4.0, buildx v0.33.0, arm64 host.

- `make docker` — passes. Image is `linux/arm64`; labels carry the full revision `94a487d7e2cbd59e46e06a0b99b01a3ddd743c1a`, the source URL and `Apache-2.0`.
- `make docker-multiarch` — passes, and the log proves the two things that matter. `pnpm build` appears exactly once, under the build platform; there is no `[linux/amd64 web …]` stage at all, so both target images copy the SPA from the same stage output and the assets are identical by construction. The Go build appears twice, `[linux/arm64 build] GOARCH=arm64` and `[linux/arm64->amd64 build] GOARCH=amd64` — the `->` is BuildKit saying it cross-compiled. No step ran emulated.
- Cross-compilation output is real: a `--platform linux/amd64` build produces `ELF 64-bit LSB executable, x86-64, statically linked`.
- Version chain with `VERSION=v0.1.0` — `-version` prints `v0.1.0`, OpenAPI `info` reports `"version": "v0.1.0"`, and the `org.opencontainers.image.version` label is `v0.1.0`. No `-dirty`, no hash.
- The migration runner still works in the distroless image: `{"msg":"applied migration","version":1,"name":"baseline"}`.
- `scripts/docker-tags.sh` — accepts `v1.4.2`, `v0.1.0`, `v10.20.30` (three tags each) and `v1.5.0-rc.1` (exact tag only); rejects `v1.4`, `1.4.2`, `v1.4.2abc`, `v1..2`, missing arguments, and — the case that matters — `94a487d-dirty`, so a `git describe` hash can never be published as `latest`.
- `make smoke-docker` — passes. `make smoke-postgres` — passes.
- The push path was proved against a throwaway `registry:3` on `127.0.0.1:5555`. `make docker-release IMAGE=127.0.0.1:5555/kilasflow VERSION=v0.1.0` pushed `v0.1.0`, `v0.1` and `latest` at one index digest; `docker buildx imagetools inspect` shows an OCI image index containing `linux/amd64`, `linux/arm64` and an attestation manifest for each. `make smoke-docker-published` then passed against that image after deleting the local tag, so it genuinely pulled and resolved through the manifest list. `docker-sign`'s digest extraction returns exactly the index digest, and the target fails with a clear message when the metadata file is absent.

### NOT verified — and why

- **Nothing has been published, and `release.yml` has never run.** A workflow that has never executed is an unverified claim. GHCR authentication, whether the `kilaslabs` package namespace accepts a first push from this repository, and whether GHCR stores the SBOM and provenance attestations are all untested. The local registry proves the buildx invocation, not the registry.
- **Signing is entirely unverified.** cosign is not installed on this machine and keyless signing needs an OIDC token only a workflow run has. `make docker-sign` has never signed anything; only its failure path was exercised. Nothing verifies these signatures either — they are produced now because retrofitting them once consumers trust unsigned images is the expensive order.
- **The `linux/amd64` image was never run.** It was built, and its binary confirmed to be x86-64, but this is an arm64 machine. The release job runs on an amd64 runner and will smoke that half; the arm64 half will then be the unexercised one until somebody runs `make smoke-docker-published` on an arm64 host.
- **The new `image` job in `ci.yml` has never run on a GitHub runner.** `make docker-multiarch` uses `--output type=cacheonly`, which worked on this machine's builder; the job adds `docker/setup-buildx-action` because the runner's default docker driver cannot emit a manifest list, but that combination is untested.
- **`make lint` and `make test` were not run** — no Go, TypeScript or Svelte source was touched.

### The first release tag was deliberately not cut

`git tag -l` is still empty. This session did not cut the tag, for a reason specific to how the work is happening rather than a judgement about the release:

- Git worktrees share one ref store. A tag created here would appear instantly in the main checkout and in every other agent session working in this repository, silently changing what `git describe` returns — and therefore the version stamped into every binary and every OpenAPI document those sessions build — in the middle of their work.
- The tag would sit on this branch's head, a commit that may never reach `main` in this form. After the merge, `git describe` would produce something like `v0.1.0-14-g<sha>` rather than a clean tag.
- Cutting it changes nothing that can be verified from here: the tag only does its job once pushed, and pushing is what triggers `release.yml`.

It is a one-line operator action on `main` after this merges:

```sh
git tag -a v0.1.0 -m "First release" && git push origin v0.1.0
```

That push is what will exercise `release.yml` for the first time and turn the unverified items above into verified ones — or not, which is exactly why they are listed.

### Stale or wrong in the ticket and its dependency

- **The GitHub repository is `kilaslabs/k-flow`, not `kilaslabs/kilas-flow`.** `FEAT-7tgasa` states "the repository is `github.com/kilaslabs/kilas-flow`"; that string is the Go module path in `go.mod`. The actual remote is `git@github.com:kilaslabs/k-flow.git`. The OCI source label and `SOURCE_URL` use `k-flow`, so the link a consumer follows resolves. The image name stays `kilasflow` — a GHCR package name need not match its repository, and `kilasflow` is what a consumer types.
- **The Implementation Plan's advice on `--platform` is wrong in a way that would have cost the ticket its main benefit.** It says to use "`TARGETARCH` with `GOARCH` rather than `--platform` on the build stage", prescribing `--platform=$BUILDPLATFORM` only for the SPA stage. Without `--platform=$BUILDPLATFORM` on the Go stage too, buildx runs that stage *at the target platform*, which means emulating the entire Go toolchain under QEMU for arm64 — precisely the cost the surrounding paragraph says it is avoiding. Both stages are pinned here; the build log confirms nothing is emulated.
- Everything else in the ticket checks out against the current tree: the three Dockerfile stages, `ARG VERSION=0.1.0`, the distroless runtime with `VOLUME`/`EXPOSE`/`USER` and four `KILASFLOW_*` defaults, the `/out/data` comment, the `Makefile` `VERSION` expression and its namespace-less `docker` target, `var version` and the `-version` flag in `cmd/kilasflow/main.go`, `huma.DefaultConfig("KilasFlow API", deps.Version)` at `internal/api/server.go:134`, both smoke-script seams, the `.dockerignore` exclusion, and an empty `git tag -l`.

### Left for other tickets

The tag vocabulary is documented at the top of `scripts/docker-tags.sh` and referenced from `release.yml`. It is not in `README.md` or any user-facing document, because this session's scope was `Dockerfile`, `.github/`, `Makefile` and `scripts/`. Surfacing `ghcr.io/kilaslabs/kilasflow` and "pin the exact `vX.Y.Z` tag" to users belongs with V2-p10-3 (`FEAT-m94hhx`), the Compose quickstart, which is the ticket that repoints Compose at a pulled image. `docker-compose.yml` still passes `VERSION: dev`, which is already honest and was left alone for the same reason.
