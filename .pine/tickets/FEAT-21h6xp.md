---
id: FEAT-21h6xp
title: Code-node JavaScript workers run as their own user, in their own namespaces, so an engine escape stays contained
status: todo
priority: medium
parent: EPIC-tjnr1z
created: "2026-09-23T07:02:59Z"
updated: "2026-09-23T07:02:59Z"
---

# Description

FEAT-g6k3y9 put Code-node JavaScript in worker processes. They contain what
goja cannot stop from inside, a built-in that runs away with a core or the
memory, but they are not a privilege boundary: a worker runs as the server's
user, with its filesystem and network. Code that escaped goja itself (which
exposes no file, process or network API to a script) would reach the
database file and the network as the server can.

The review of FEAT-g6k3y9 (2026-09-23) also noted that workers are shared
across tenants for up to 1000 jobs. Every frame now carries its job's nonce,
and a worker that writes past its result is retired, but an escaped worker
would still see later tenants' jobs.

# Acceptance Criteria
- [ ] On Linux a worker runs as a different user from the server (a
      configured UID/GID, or a user namespace), with no read access to the
      server's data directory or configuration.
- [ ] A worker has no network (a network namespace, or seccomp denying
      socket()), no new file opens beyond what the Go runtime needs at start
      (landlock or seccomp), and no ptrace.
- [ ] Optionally, workers are kept per tenant, so a worker never runs two
      tenants' jobs; measure the cold-start cost first.
- [ ] Deployments that cannot grant what this needs (a container without
      user namespaces or CAP_SETUID) keep today's behaviour and log it once.
- [ ] docs/concepts/safety-boundaries.md stops saying the workers are not a
      privilege boundary, and says what they are.

# Implementation Plan

# Notes

# Related Files

# Attachments
