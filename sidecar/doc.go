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
// ships no JavaScript runtime, no node_modules tree, and no community package:
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
// one.
//
// One connection carries sequential executes: the host sends one execute
// frame, the child answers exactly one result or error frame with the same
// id. Any other child-to-host frame is a host call; none is implemented at
// this layer, so the first one fails the run with host-call-denied. That is
// the deny-by-default form of the ticket's recommendation: outbound HTTP from
// a community node must be proxied back through the host's egress policy, and
// until that proxy exists the boundary refuses rather than silently widening.
//
// # Lifecycle
//
// One child process serves exactly one tenant (Pool is keyed by tenant),
// because decrypted secrets cross the socket into third-party JavaScript and
// process isolation is the only isolation left once they do. A claimed
// execution for tenant B never reaches a process started for tenant A. Idle
// processes are reaped by CloseIdle; there are no background goroutines —
// the operator ticks that on a timer when the engine owns a pool.
package sidecar
