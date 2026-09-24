---
id: FEAT-0ynje5
title: 'Code-node JavaScript workers: an optional seccomp profile authored and reviewed by a person'
status: todo
priority: low
parent: EPIC-tjnr1z
created: "2026-09-24T13:51:58Z"
updated: "2026-09-24T13:51:58Z"
---

# Description

FEAT-21h6xp confines Code-node JavaScript workers on Linux with namespaces
(user, PID, network, IPC), an optional configured worker user, landlock and
PR_SET_DUMPABLE=0. It deliberately adds no seccomp filter: the controller
ruled (2026-09-24) that a system-call profile is left for a change authored
and reviewed by a person, not written by an agent.

A reviewed profile would add a layer where the others are missing or do not
reach: kernels without landlock, containers that refuse user namespaces
(where a worker could still change the server's resource limits), connecting
to Unix sockets by path, and kernel surface a worker never needs.

# Acceptance Criteria
- [ ] A person writes and reviews the profile, and records why each denied
      family is safe for the Go runtime of the worker.
- [ ] It is optional and logged with the other layers, like them.
- [ ] Workers keep passing internal/jsworker's Linux confinement tests.

# Notes

# Related Files
- internal/jsworker/confine_linux.go
- docs/src/content/docs/concepts/safety-boundaries.md
