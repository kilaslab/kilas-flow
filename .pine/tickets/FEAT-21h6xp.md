---
id: FEAT-21h6xp
title: Code-node JavaScript workers run as their own user, in their own namespaces, so an engine escape stays contained
status: done
priority: medium
parent: EPIC-tjnr1z
created: "2026-09-23T07:02:59Z"
updated: "2026-09-24T14:25:09Z"
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
- [x] On Linux a worker runs as a different user from the server (a
      configured UID/GID, or a user namespace), with no read access to the
      server's data directory or configuration.
- [x] A worker has no network (a network namespace, or seccomp denying
      socket()), no new file opens beyond what the Go runtime needs at start
      (landlock or seccomp), and no ptrace.
- [ ] Optionally, workers are kept per tenant, so a worker never runs two
      tenants' jobs; measure the cold-start cost first. (Measured; not
      done here, filed as FEAT-f40kg4. See Decisions.)
- [x] Deployments that cannot grant what this needs (a container without
      user namespaces or CAP_SETUID) keep today's behaviour and log it once.
- [x] docs/concepts/safety-boundaries.md stops saying the workers are not a
      privilege boundary, and says what they are.

# Implementation Plan

# Notes

## Plan (2026-09-24, revised by the controller's ruling the same day)

Task 7 of the EPIC-tjnr1z remaining-work plan. Scope kept to process spawn
attributes, the worker's own start-up confinement, config, docs and tests.
The first plan put a hand-written seccomp filter in the worker. The
controller ruled that this task writes **no system-call filter at all**, and
that it meets the criteria with kernel isolation that needs no system-call
list. The layers are below. A system-call profile is left for a change a
person writes and reviews: FEAT-0ynje5.

## Decisions

- **Spawn ladder (server side, `confine_linux.go` `spawnProfiles`).**
  Strongest first. Only a kernel refusal (EPERM, EACCES, EINVAL, ENOSPC,
  ENOSYS, EUSERS) steps down, once per pool, and the pool keeps what it got.
  - No worker user configured: `CLONE_NEWUSER|NEWPID|NEWNET|NEWIPC`, then
    today's plain start.
  - `code.javascript_worker_uid`/`_gid` configured: `Credential`
    (supplementary groups cleared) plus `NEWPID|NEWNET|NEWIPC`, then the
    credential alone. It never falls back to the server's user (fail
    closed), because the operator asked for that user.
  - Every profile also gets `Setsid`: the worker has a session and process
    group of its own, so kill(0) reaches only itself.
- **No ID mapping.** Mapping 65534 to the server's uid failed with EACCES
  for a non-root server. The server is undumpable, so a forked child's
  /proc/<pid>/uid_map is root's to write until the child execs. Unmapped,
  the worker is the kernel's overflow user (65534) in its namespace and keeps
  no capability after exec. Root passed either way, which is why only the
  non-root container run caught it.
- **Landlock (worker side, `confineSelf`, before the first job).**
  - It handles every filesystem right the kernel's ABI knows, and grants only
    READ_FILE/READ_DIR beneath Go's zoneinfo sources ($ZONEINFO included),
    so Intl and luxon zones resolve as they do in the server. `time.Local` is
    loaded before the domain is entered.
  - From ABI 4 it denies TCP bind and connect; from ABI 6 it scopes signals
    and abstract Unix sockets. Landlock also keeps a domain from tracing a
    process outside it.
  - A landlock domain belongs to one thread. The worker restricts every
    thread with `LANDLOCK_RESTRICT_SELF_TSYNC` where the kernel has it, and
    otherwise with `syscall.AllThreadsSyscall`, after no_new_privs on every
    thread. A cgo build on an older kernel cannot do this, and logs landlock
    as missing.
  - A test forces the per-thread path and reads from 16 locked threads. With
    only the calling thread restricted it fails (checked).
- **PR_SET_DUMPABLE=0** in the worker, kept.
- **How each criterion is met without seccomp:**
  - Different user: a user namespace, or the configured UID/GID.
  - No read access to the data directory or config: landlock, plus DAC for a
    configured user.
  - No network: the network namespace (loopback, down), plus landlock TCP.
  - No new file opens: landlock.
  - No ptrace: the PID namespace (no number to name), landlock's ptrace
    scoping, and undumpable.
  - The prlimit64 finding (the unconfined probe set the server's RLIMIT_CPU
    to 0 and killed it): the PID namespace, or a configured user.
- **Fallback.** A container that refuses user namespaces (Docker's default
  seccomp profile) still gets landlock and undumpable, and logs WARN "only
  partly confined" with why. macOS logs "not confined, Linux only".
- **The ready frame.** The worker answers hello with `ready`, carrying what
  it installed, before it reads a job. The server waits up to 10 s in
  `start()` and logs once per pool, and again only if what it logs changes.
  The report is only logged, never trusted.
- **Per-tenant workers: measured, not done.** Cold start is 7.9 ms against
  1.1 ms warm (Linux arm64 container; 7.65 ms without the namespaces), and
  10.7 ms against 0.9 ms on macOS. It is cheap to pay, but not cheap to
  build: the tenant would have to be carried through `jsrun.Task` and the
  pool would have to evict other tenants' idle workers. Filed as
  FEAT-f40kg4.
- **Known gaps (documented in safety-boundaries.md):**
  - In a user namespace the worker is still the server's uid to the host's
    filesystem, so without landlock only a configured user closes the files.
  - Landlock does not govern connecting to pathname Unix sockets.
  - Without a PID namespace, prlimit on the server stays possible.

## Progress (2026-09-24)

Done on epic/feat-21h6xp, rebased on main after FEAT-x9gq0s merged.
FEAT-x9gq0s's in-process `serve` test now passes the confinement in, so the
test's own process is never confined.

Linux runs (golang:1.27-bookworm and distroless static nonroot, OrbStack
kernel 7.0, arm64), each run 1-3 times, all PASS:

| Run | Result |
| --- | --- |
| privileged root (`JSWORKER_EXPECT_CONFINED=1`) | every layer |
| Docker default, root | user namespace refused (EPERM), landlock and undumpable active, logged once |
| non-root with seccomp unconfined | every layer, unprivileged user namespace |
| distroless nonroot, default | fallback |
| distroless nonroot, seccomp unconfined | every layer |
| `-race` privileged | every layer; landlock through TSYNC in a cgo build |

`go test ./...` passes on macOS. On Linux, nodes, cmd, jsworker and config
pass. `internal/jsrun`'s `TestZoneNamesMatchNodeForEveryZone` fails in the
container on main as well, because Debian's tzdata stores Europe/Dublin in
the vanguard form. It runs in-process and has nothing to do with workers.
Filed as a bug under the epic.

Follow-ups: FEAT-0ynje5 (a seccomp profile written and reviewed by a person),
FEAT-f40kg4 (per-tenant workers).

## Review fixes (2026-09-24, round 1)

1. **Step-down.** A stronger profile is given up only once a weaker one has
   actually started a worker. A start that fails with every profile (a
   binary the worker may not run, where exec and a clone refusal both come
   back as the same errno) keeps the strongest. A refused profile is asked
   for again every 10 minutes (`reprobeAfter`), and the recovery is logged.
   Both behaviours are tested.
2. **Docs.** Signal scoping needs landlock ABI 6 (Linux 6.12). Without a PID
   namespace or a worker user, which is the distroless image under Docker's
   default seccomp profile, an escaped worker on 5.15/6.1/6.6 could signal
   the server, and on any kernel it could change the server's limits.
   safety-boundaries.md, deployment.md and doc.go now say so, and recommend
   a worker user or allowing user namespaces.
3. **ID bounds.** Worker IDs are at most 1<<32-2, checked in config and in
   the pool. Past that a uint32 cast would wrap to root. The server's own
   euid is refused as a worker user.
4. **Boot check.** `Pool.Start` starts one worker at boot when a worker user
   is configured. `cmd/kilasflow` refuses to boot, naming the keys, when the
   server cannot start a worker as that user. The error and the docs say
   what it needs: CAP_SETUID/CAP_SETGID and the binary executable by that
   user.
5. **Zone rule failure.** A zoneinfo rule that cannot be added leaves
   landlock on, without that rule, and the active entry says so.
6. **Probe and tests.**
   - The probe no longer records socket creation, which reaches nothing. It
     asserts dials to TCP and to an abstract Unix socket, and in the
     landlock-only case it conditions dial (ABI >= 4) and signals and the
     abstract socket (ABI >= 6) on what the worker reports.
   - The stop-signal test is not vacuous: removing `signal.Ignore` makes it
     fail with exit 129, even in a PID namespace. It now also asserts the
     worker's own process group.
   - `awaitReady` honours the run's context.
   - The configured-user row says "not world-readable".
   - Commits were regrouped so every commit builds.

## Review fixes (2026-09-24, round 2)

1. **Settling.** A profile is settled only after its worker says it is
   ready. `start` launches (spawn, hello, `awaitReady`) and settles on
   success.
2. **Re-probes.** A re-probe of a stronger profile never costs a run.
   Whatever it meets (never ready, ENOENT, EAGAIN…), the run starts with the
   settled profile, which stays the pool's, and the earlier refusal is kept.
   Tested cross-platform with a profile that starts a worker that never says
   it is ready, and one whose binary does not exist.
3. **Time zone database.** A database landlock could not allow is reported
   under `missing` ("time zone database: …"), so the log is a WARN.
4. **deployment.md** names prlimit on any kernel beside signals before
   Linux 6.12.
5. **Probe ABI.** The probe test reads the landlock ABI from the kernel
   itself and checks the worker's report against it.

# Related Files
- internal/jsworker/confine.go, confine_linux.go, limits_linux.go, limits_other.go, pool.go, protocol.go, worker.go, doc.go
- internal/jsworker/confine_test.go, confine_linux_test.go
- internal/config/config.go (code.javascript_worker_uid/_gid)
- docs/src/content/docs/concepts/safety-boundaries.md, operate/deployment.md

# Attachments

## Work Evidence

Closed by `pine close --evidence` on 2026-09-24.

- Base: `1fef8ed3` (last commit at or before ticket created 2026-09-23)
- Commits (12):
  - `4f2d4376` — merge: FEAT-21h6xp Code-node JavaScript workers are confined on Linux: their own user, namespaces and landlock
  - `442794f9` — FEAT-21h6xp: deployment names a worker's reach over the server's limits on any kernel, and the ticket records the second review's fixes
  - `b1813cc4` — FEAT-21h6xp: a time zone database landlock could not allow is logged as missing, and the probe reads the landlock ABI from the kernel
  - `348eca4f` — FEAT-21h6xp: a profile settles only once its worker says it is ready, and asking again for a stronger one never costs a run
  - `31ee3ac0` — FEAT-21h6xp: the probe does not count a container's first process group, which a worker cannot name, as the server's
  - `5560544b` — FEAT-21h6xp: the docs say which kernels scope a worker's signals and what closes the gap, and the ticket records the review fixes
  - `5deaf667` — FEAT-21h6xp: a time zone database that cannot be allowed leaves the files closed, a run cancelled while its worker starts ends at once, and the probe asserts what each landlock ABI covers
  - `ae1ea141` — FEAT-21h6xp: a worker user past 32 bits or the server's own is refused, and one the server cannot start workers as stops the boot
  - `198c973b` — FEAT-21h6xp: a stronger start is given up only once a weaker one starts, and is asked for again every ten minutes
  - `a39831a1` — FEAT-21h6xp: the safety boundaries say what confines a worker and what does not, and the ticket moves to testing with its decisions and follow-ups
  - `b83ce902` — FEAT-21h6xp: code.javascript_worker_uid and code.javascript_worker_gid run every JavaScript worker as a user of its own
  - `808bff71` — FEAT-21h6xp: on Linux a JavaScript worker starts in user, PID, network and IPC namespaces of its own and locks itself out of every file, TCP connection and outside signal before its first job
- Files changed (the ticket's own commits, 1c516014ea5568902c031a8708cc67c4daa422fb..4f2d43765214fbd5a1564207d3edc5540054a3e8):

```
 .pine/tickets/BUG-a9d2hb.md                              |  33 +++
 .pine/tickets/FEAT-0ynje5.md                             |  34 +++
 .pine/tickets/FEAT-21h6xp.md                             | 171 +++++++++++-
 .pine/tickets/FEAT-f40kg4.md                             |  37 +++
 cmd/kilasflow/javascript_test.go                         |  29 ++
 cmd/kilasflow/main.go                                    |  27 +-
 config.example.yaml                                      |  18 ++
 docs/src/content/docs/concepts/safety-boundaries.md      |  67 ++++-
 docs/src/content/docs/operate/configuration-reference.md |  30 +++
 docs/src/content/docs/operate/deployment.md              |  14 +
 go.mod                                                   |   2 +-
 internal/config/config.go                                |  54 ++++
 internal/config/config_test.go                           |  31 +++
 internal/jsworker/confine.go                             | 192 ++++++++++++++
 internal/jsworker/confine_linux.go                       | 234 ++++++++++++++++
 internal/jsworker/confine_linux_test.go                  | 501 +++++++++++++++++++++++++++++++++++
 internal/jsworker/confine_test.go                        | 234 ++++++++++++++++
 internal/jsworker/doc.go                                 |  25 +-
 internal/jsworker/helpers_test.go                        |  13 +-
 internal/jsworker/jsworker_test.go                       |  90 ++++++-
 internal/jsworker/limits_linux.go                        |  12 +-
 internal/jsworker/limits_other.go                        |  22 +-
 internal/jsworker/pool.go                                | 175 +++++++++++-
 internal/jsworker/probe_other_test.go                    |   6 +
 internal/jsworker/protocol.go                            |  12 +-
 internal/jsworker/worker.go                              |  19 +-
 26 files changed, 2018 insertions(+), 64 deletions(-)
```
