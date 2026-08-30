# syntax=docker/dockerfile:1

# ---- Stage 1: build the SPA -------------------------------------------------
FROM node:24.16-alpine AS web

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
FROM golang:1.27-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# The SPA must land in internal/web/dist before compiling: internal/web/embed.go
# embeds that directory into the binary.
RUN rm -rf internal/web/dist
COPY --from=web /src/web/build ./internal/web/dist

ARG VERSION=0.1.0
RUN CGO_ENABLED=0 go build \
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
FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /app

COPY --from=build /out/kilasflow /app/kilasflow
COPY --from=build --chown=nonroot:nonroot /out/data /app/data

# SQLite database and any user data.
VOLUME ["/app/data"]

ENV KILASFLOW_SERVER_HOST=0.0.0.0 \
    KILASFLOW_SERVER_PORT=8080 \
    KILASFLOW_DATABASE_DSN=/app/data/kilasflow.db \
    KILASFLOW_LOG_FORMAT=json

EXPOSE 8080

USER nonroot:nonroot

ENTRYPOINT ["/app/kilasflow"]
