// Package jsrun runs the JavaScript of an n8n Code node on goja, a pure-Go
// ECMAScript engine. No Node.js process is involved at any point. The server
// runs it in worker processes (internal/jsworker): it prepares a job here and
// decodes its result here, and a worker executes it; tests and tools run all
// three steps in one process with Runner.Run.
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
// imports the filesystem, the network or a process; internal/guardrails holds
// that rule. What a script asks of the server beyond its input, n8n's
// this.helpers (httpRequest, getBinaryDataBuffer, prepareBinaryData) and
// $getWorkflowStaticData, is carried out by the node that runs it, through
// the Helpers it puts in the roots: the request goes out from the server
// under the deployment's egress policy, and files and static data are the
// execution's own. In a worker the calls cross the worker protocol. Any
// other helper is refused in the one sentence.
//
// # Helpers
//
// A helper is one asynchronous host call. The runtime hands the request to a
// goroutine and returns a promise; the answer comes back through the
// execution's job loop, the only place a promise may be settled, so the code
// runs its timers and other promises while it waits, and several calls can
// be in flight at once. Every call counts against Limits.MaxHostCalls. A
// failed request rejects its promise; a named limit (a file or response too
// large) stops the code. The static data the code was handed is written back
// as JSON after a successful run, within MaxStaticDataBytes, and the files it
// stored are the only files beyond its input that a returned item may name.
// A file a returned item gives inline, as base64 text in its binary entry's
// data, is stored the same way once the run has succeeded, by the process
// that prepared the job, and counts as one host call.
//
// # Time
//
// The time limit covers the user's program only, which is the BUG-9s3htg rule
// restated for this engine (.pine/memory/code-node.md). Creating the VM,
// loading a library, parsing the input and decoding the output are not
// charged, and nor is time the code spends idle waiting on nothing but the
// server's answer to a helper; the execution's context and the helper's own
// timeout bound that wait instead. Every VM entry from the first user
// statement on is charged, including the result's normalisation and
// JSON.stringify, because a script's toJSON, getters and Proxy traps run
// inside those. In "Run once for each item" mode the items share one budget,
// and the clock is paused between them.
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
// so memory is bounded in layers:
//
//   - the input and output are capped, and so is the number of scripts
//     running at once;
//   - a process-wide watchdog stops every running script with ErrMemoryLimit
//     once the live heap passes a ceiling, and the process survives;
//   - built-ins that allocate or loop as far as a number tells them refuse a
//     huge one up front: the array methods, Array.from, argument lists, typed
//     arrays, repeat and padding (MaxElementsPerCall and its siblings).
//
// goja cannot interrupt one built-in call, and the watchdog can only
// interrupt, so between samples one call can still grow the heap. The worst
// case is the ceiling plus, for each script running at once, one call's
// growth. The per-call bounds keep that to around a hundred MiB for the
// built-ins that allocate from a number. A built-in that works over data the
// script already holds, such as split or JSON.stringify, grows it by a small
// factor of what the ceiling already allowed. In the server that process is a
// worker, whose address-space limit and deadline the pool enforces, so what
// one call can still do costs a worker, not the server.
//
// # Source
//
// goja's parser and compiler recurse on nesting and fold constants with no
// limits of their own, and a Go stack overflow is fatal to the whole process.
// A body is therefore bounded before goja sees it: its length, its arrow
// functions, the depth of its tree and of its constant expressions (see
// MaxSourceBytes), and the size of the BigInt constants goja would work out.
// Parsing and compiling, which cannot be interrupted, run at most GOMAXPROCS
// at a time. Analyze, which the server runs to validate and import a body,
// parses and inspects it without compiling it.
package jsrun
