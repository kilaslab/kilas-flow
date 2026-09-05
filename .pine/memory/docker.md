---
topic: docker
updated: 2026-09-05T18:03:41Z
---

# docker

- 2026-09-05: Compose v2 resolves compose.yaml before docker-compose.yml, so adding compose.yaml silently changes what every bare 'docker compose' in the repo runs. Any script that reaches a service through default file resolution — scripts/smoke-postgres.sh did, via --profile postgres — must name its files with -f explicitly, or it will quietly start proving a different stack than the one it thinks it is proving.
- 2026-09-05: The runtime image is gcr.io/distroless/static-debian12:nonroot — no shell, no curl, no wget — and cmd/kilasflow has only -config and -version flags. A Docker HEALTHCHECK or a compose healthcheck on the app service therefore cannot be written: every expressible form either always fails or passes the instant the container starts, and the second is worse than none because depends_on: service_healthy would then report a server ready before it opened its listener. Assert readiness from outside with GET /api/v1/ready, which is what scripts/smoke-docker.sh does. Giving the binary a probe subcommand is the fix if a real healthcheck is ever needed.
