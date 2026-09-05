# syntax=docker/dockerfile:1

# ---- Stage 1: build the SPA -------------------------------------------------
# Pinned to the build platform so a two-architecture build runs this stage once
# on the native runner rather than a second time under emulation. Its output is
# JavaScript and has no architecture of its own, so both published images embed
# byte-identical frontend assets from this single execution.
FROM --platform=$BUILDPLATFORM node:24.16-alpine AS web

WORKDIR /src/web

RUN corepack enable

# Copy the manifests first so the dependency layer is cached independently of
# application source changes. scripts/ comes with them because postinstall runs
# vendor-docs.mjs, which copies the Scalar bundle out of node_modules.
COPY web/package.json web/pnpm-lock.yaml ./
COPY web/scripts ./scripts
RUN pnpm install --frozen-lockfile

COPY web/ ./
RUN pnpm build


# ---- Stage 2: build the Go binary -------------------------------------------
# Also pinned to the build platform. The compiler runs natively and cross-compiles
# through GOARCH below, so producing the arm64 image never emulates the Go
# toolchain — minutes of QEMU per release avoided for nothing given up, because
# CGO is off and the build has no architecture-dependent inputs. Emulating this
# stage instead would be the obvious alternative and is strictly worse.
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# The SPA must land in internal/web/dist before compiling: internal/web/embed.go
# embeds that directory into the binary.
RUN rm -rf internal/web/dist
COPY --from=web /src/web/build ./internal/web/dist

# BuildKit supplies TARGETOS and TARGETARCH for whichever platform is being
# produced, but predefined arguments are stage-scoped and have to be re-declared
# in the stage that reads them. Both are empty when this file is built by
# something that does not set them, and an empty GOOS or GOARCH means "the
# toolchain default" — which is the single-architecture behaviour this stage had
# before, so nothing regresses.
ARG TARGETOS
ARG TARGETARCH

# The default is deliberately not a plausible release number. An image built with
# no --build-arg VERSION is a development build and must say so, rather than claim
# a version somebody could look up in the release notes; the previous default of
# 0.1.0 was indistinguishable from a real one.
ARG VERSION=0.0.0-dev
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
    -trimpath \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/kilasflow \
    ./cmd/kilasflow

# The runtime image is distroless and has no shell, so the data directory has
# to be created here and copied in with the right ownership. kilasflow runs as
# nonroot and creates ./data itself on first start, which it cannot do inside a
# root-owned /app.
RUN mkdir -p /out/data


# ---- Stage 3: runtime -------------------------------------------------------
# Distroless works because kilasflow builds with CGO disabled end to end, including
# SQLite (glebarez/sqlite is pure Go).
#
# This stage carries no RUN, which is what lets a multi-architecture build skip
# QEMU entirely: nothing is ever executed for the target architecture, only copied
# into place. Adding a RUN here would silently make every release depend on an
# emulator — prefer doing the work in the build stage above.
FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /app

COPY --from=build /out/kilasflow /app/kilasflow
COPY --from=build --chown=nonroot:nonroot /out/data /app/data

# Provenance a consumer can read off a running container, so tracing one back to
# the commit it was built from needs nobody's help. REVISION and SOURCE are
# arguments rather than derived here because .dockerignore excludes .git, so the
# build context has no repository to interrogate — whoever builds must say. The
# defaults name an unknown revision instead of guessing a plausible one.
#
# Below the COPY lines rather than above them: REVISION changes on every commit,
# and declaring it earlier would invalidate the binary layer for a metadata-only
# difference.
ARG VERSION=0.0.0-dev
ARG REVISION=unknown
ARG SOURCE=https://github.com/kilaslabs/k-flow
LABEL org.opencontainers.image.title="KilasFlow" \
      org.opencontainers.image.description="Workflow automation server with an embedded SPA" \
      org.opencontainers.image.source="${SOURCE}" \
      org.opencontainers.image.revision="${REVISION}" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.licenses="Apache-2.0"

# SQLite database and any user data.
VOLUME ["/app/data"]

ENV KILASFLOW_SERVER_HOST=0.0.0.0 \
    KILASFLOW_SERVER_PORT=8080 \
    KILASFLOW_DATABASE_DSN=/app/data/kilasflow.db \
    KILASFLOW_LOG_FORMAT=json

EXPOSE 8080

USER nonroot:nonroot

ENTRYPOINT ["/app/kilasflow"]
