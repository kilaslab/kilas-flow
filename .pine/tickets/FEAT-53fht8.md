---
id: FEAT-53fht8
title: Publish multi-architecture container images
status: todo
priority: high
labels:
    - release
    - platform
deps:
    - FEAT-7tgasa
parent: EPIC-m42s3g
phase: p10
created: "2026-09-05T11:46:58Z"
updated: "2026-09-05T11:46:58Z"
---

## Scope

Nothing this repository builds is published anywhere, and the version it stamps into what it builds is a dirty commit hash.

`git tag -l` is empty. `Makefile` sets `VERSION ?= $(shell git describe --tags --always --dirty || echo "0.1.0-dev")`, which with no tags resolves to something like `21056a1-dirty`. That value reaches three places that matter: the binary through `-ldflags "-X main.version=$(VERSION)"`, the `-version` flag, and — because `internal/api/server.go` passes `deps.Version` into `huma.DefaultConfig("KilasFlow API", deps.Version)` — the `info.version` field of the OpenAPI document every generated client is built from. A consumer asking the API what version it is talking to is told a commit hash with `-dirty` appended.

The image itself is well built and single-architecture. The `Dockerfile` has three stages: `node:24.16-alpine` builds the SPA, `golang:1.27-alpine` copies `web/build` into `internal/web/dist` and compiles with `CGO_ENABLED=0 -trimpath`, and the runtime is `gcr.io/distroless/static-debian12:nonroot` with `VOLUME ["/app/data"]`, `EXPOSE 8080`, `USER nonroot:nonroot` and four `KILASFLOW_*` defaults baked in. There is no `--platform`, no `TARGETPLATFORM` or `TARGETARCH`, no buildx bake file and no `GOARCH` handling anywhere in it, so it produces whatever the building daemon happens to be. `make docker` tags `kilasflow:$(VERSION)` and `kilasflow:latest` — bare names with no registry namespace — and nothing ever pushes.

The cost of fixing this is unusually low, which is the argument for doing it properly now rather than later. The build is already `CGO_ENABLED=0` onto a static distroless base, so cross-compiling is a `GOARCH` argument and not a toolchain project. The only stage that is genuinely architecture-dependent is the Node SPA build, and its output is architecture-independent JavaScript, so it can run once on the native runner and be shared by both targets.

## Acceptance criteria

- [ ] A published image runs on `linux/amd64` and `linux/arm64`, and pulling by one tag on either architecture resolves through a manifest list to the right one.
- [ ] The SPA build stage executes once per build rather than once per target architecture, and the two architecture images embed byte-identical frontend assets.
- [ ] A git tag produces an image whose `main.version`, `-version` output and OpenAPI `info.version` are all that tag, with no `-dirty` suffix and no commit hash.
- [ ] The tag vocabulary is documented and implemented — an immutable exact version, a moving minor tag, and `latest` — and it is stated which of them a production deployment should pin.
- [ ] The image carries OCI source, revision, version and licence labels, and a consumer can trace a running container back to the commit it was built from without asking anyone.
- [ ] `make docker` and the published pipeline build the same image from the same Dockerfile, so a local reproduction of a published image is possible.
- [ ] `smoke-docker` passes against a pulled published image, not only against a locally built one.
- [ ] The first release tag is cut as part of this ticket, so `git describe` stops resolving to a commit hash for every subsequent build.

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
