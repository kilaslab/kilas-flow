// Package sidecar is the host side of the JavaScript sidecar boundary.
//
// A programmatic community node runs in a Node.js process outside the
// kilasflow binary; this package is the only thing the host knows about it.
// The shape follows the Code node's isolation pattern (internal/runcode):
// untrusted code never runs in this process, every limit is enforced on the
// host side of the boundary, and exceeding one fails that node run with a
// named error — never the workflow, never the host process.
//
// # Licence position
//
// The sidecar is operator-installed, never KilasFlow-distributed. KilasFlow
// ships no Node.js runtime, no node_modules tree, and no community package:
// the operator installs Node 24 LTS and the packages they choose, and points
// the deployment at a script. The protocol below is clean-room — NDJSON
// frames over a Unix socket, described here from scratch — so nothing in this
// package derives from, links, or loads Sustainable-Use-Licensed code. A
// deployment that declines the sidecar loses nothing it has today: Pool
// without a spawner fails sidecar-tagged runs with a diagnostic naming what
// the operator must provide, exactly the way the Code node behaves without a
// Go toolchain.
//
// # Protocol
//
// Host and child speak NDJSON (one JSON object per line) over SOCK_STREAM on
// a Unix socket in the instance's data directory. The socket path reaches the
// child as an argument; the child's stdout and stderr are connected to the
// host log as diagnostics only. Nothing the protocol depends on is ever read
// from those streams, so a package that prints a startup banner cannot
// corrupt a single message — the fixture under fixture/ proves it by printing
// one, a syntactically valid forged result frame, and megabytes of stderr
// noise before answering honestly on the socket.
//
// One connection carries sequential executes: the host sends one execute
// frame; the child answers exactly one result or error frame with the same
// id. The answer may carry one item list per output port (Outputs), and when
// it carries only Items that is treated as a single output port. Host calls
// run the other way: the child asks the host to perform an outbound HTTP
// request, the host answers with http.response or http.error, and the calls
// are served one at a time inside the run's read loop. Every other child
// frame is denied with host-call-denied. That is the deny-by-default form of
// the ticket's rule: outbound HTTP from a community node must be proxied back
// through the host's egress policy, and this layer refuses rather than
// silently widening until that proxy is wired. A nil HostHandler denies every
// call, which is the deployment's default.
//
// # Process posture
//
// The child is started with an explicit environment allowlist (LANG and TZ,
// nothing else — the host's credential master key and database DSN never
// cross) and under Node's permission model: --permission plus one
// --allow-fs-read grant per allowed path, and no --allow-child-process,
// --allow-worker, --allow-addons, --allow-wasi, --allow-inspector or
// --allow-fs-write. The script and every read path are resolved through
// symlinks before the grant is built, because Node refuses to start when a
// component of a granted path is a symlink. The listener is created before
// the child starts, so a child that dials immediately cannot lose the race.
// The child runs in its own process group, so killing one tenant's sidecar
// kills the whole tree it spawned.
//
// # Limits
//
// Every limit is host-side. Timeout bounds one round trip; SpawnTimeout
// bounds a cold start; MaxFrameBytes bounds one NDJSON line; MaxOutputBytes
// bounds the decoded result payload, charging both items and outputs so a
// multi-output answer cannot move its bytes past the limit. MaxRSSMB is a
// resident-set watchdog, because --max-old-space-size (NodeMaxHeapMB) bounds
// only V8's old space and Buffer allocations live outside it. MaxProcesses
// caps the pool; at the cap an idle process is evicted first, and only when
// every process is busy does a run wait and then fail as sidecar-busy.
// MaxHostCalls caps host calls per run; MaxCatalogueBytes caps a describe
// answer.
//
// # Lifecycle
//
// One child process serves exactly one tenant (Pool is keyed by tenant),
// because decrypted secrets cross the socket into third-party JavaScript and
// process isolation is the only isolation left once they do. A claimed
// execution for tenant B never reaches a process started for tenant A, and a
// cold start is single-flight: concurrent runs for one tenant share one
// process. A run whose context is cancelled or whose deadline passes kills
// and evicts the process, because a child mid-run is untrusted. Idle
// processes are reaped by CloseIdle; there are no background goroutines — the
// operator ticks that on a timer when the engine owns a pool. Discover runs
// the same process in a describe role to read the package catalogue, with an
// empty tenant that Execute can never select.
//
// # Honesty
//
// The JS-side guard (Stage 2) and Node's permission model are defence in
// depth, not a sandbox against malicious code. A package sharing the host's
// uid can still signal the host process and other tenants' sidecars, and a
// package that busy-loops past a SIGKILL escapes its socket close. A hostile
// package model needs a separate uid or namespace (sidecar.wrapper) or a
// container policy; this package documents that boundary rather than claiming
// it.
package sidecar
