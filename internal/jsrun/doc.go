// Package jsrun runs the JavaScript of an n8n Code node inside the server
// process, on goja, a pure-Go ECMAScript engine. No Node.js process is
// involved at any point.
//
// A body runs as JavaScript or is refused; it is never translated into
// anything else. What cannot run faithfully is found before it runs, by
// Analyze, and refused through one sentence, Refusal, so the importer, the
// node's validation and a run all say the same thing.
//
// # Isolation
//
// Every execution gets a fresh VM, discarded afterwards. A VM costs tens of
// microseconds, so a script that patches Array.prototype or leaves a global
// behind cannot reach the next execution, let alone another tenant's. Only
// compiled programs are shared, and goja documents those as safe to run in
// many VMs at once.
//
// A script can reach exactly the globals this package installs. Nothing here
// imports the filesystem, the network or a process; a capability a script is
// granted, such as outbound HTTP, arrives as a Go function from the node that
// runs it. internal/guardrails holds both rules.
//
// # Time
//
// The time limit covers the user's program only, which is the BUG-9s3htg rule
// restated for this engine (.pine/memory/code-node.md). Creating the VM,
// loading a library, parsing the input and decoding the output are not
// charged. Every VM entry from the first user statement on is charged,
// including the result's normalisation and JSON.stringify, because a
// script's toJSON, getters and Proxy traps run inside those. In "Run once for
// each item" mode the items share one budget, and the clock is paused between
// them.
//
// A backtracking regular expression is the one piece of work an interrupt
// cannot stop. goja reports a regexp2 match timeout as "no match", which would
// silently change what a script computes. So the match timeout is set to at
// least the time-limit ceiling: by the time a match can time out, the
// interrupt has already been delivered, and the script is stopped before it
// can use the wrong answer. The cost is that such a regex can overrun its
// limit by up to the ceiling.
//
// # Memory
//
// goja keeps JavaScript objects on the Go heap and has no per-VM accounting,
// so memory is bounded in layers: the input and output are capped, the number
// of concurrent scripts is capped, and a process-wide watchdog stops every
// running script with ErrMemoryLimit once the live heap passes a ceiling. The
// scripts fail and the server survives. The worst case is the ceiling plus one
// allocation made by a single built-in, which the watchdog cannot interrupt.
package jsrun
