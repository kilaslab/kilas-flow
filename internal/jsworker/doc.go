// Package jsworker runs Code-node JavaScript in worker processes.
//
// goja cannot interrupt a single built-in call, and the runtime's heap
// watchdog can only interrupt. A built-in that allocates or loops as far as a
// number tells it could therefore hold a core, or take the memory of the
// whole process, long past any limit, and internal/jsrun's in-process bounds
// on such built-ins are a denylist: they turn the known cases into a
// RangeError, and cannot promise there is no other. So the server runs every
// script in a worker: the kilasflow binary itself, started again as a worker
// (see IsWorker and Serve). A runaway built-in then costs one worker, which
// the pool kills and replaces, and never the server.
//
// A Pool holds at most MaxConcurrent workers, starts them when needed and
// reuses them; each runs one job at a time, on a fresh VM, as the in-process
// runner does. The server prepares the job (jsrun.Runner.Prepare: the input
// is encoded and checked against its cap, and nothing is compiled or run in
// the server), hands it to a worker over the worker's stdin, and answers the
// questions the code asks while it runs over the same pipes: $('Node') and
// the static data at once, and a helper such as this.helpers.httpRequest,
// which the server itself carries out, on a goroutine of its own, while the
// code runs on. A goroutine in the worker routes each reply to its call by
// ID, so several can be outstanding at once. The worker's own clock and
// watchdog stop a script as they do in process. Beyond them:
//
//   - a worker still running well past its job's time limit is killed, and
//     the run fails with the time-limit error; time the worker spends
//     waiting on the server's answer to a helper does not count;
//   - cancelling the execution kills the worker running it;
//   - a worker that dies mid-run fails the run with a named error: the
//     memory limit when it ran out of memory or was killed by the kernel,
//     an engine fault otherwise;
//   - on Linux each worker has an address-space limit, asks the kernel to
//     kill it first when memory runs out (oom_score_adj 1000), runs with
//     no_new_privs, and dies with the server (a parent-death signal);
//   - a worker's environment holds only the marker that makes it one, its
//     runtime settings and the time zone, none of the server's configuration
//     or secrets; on Linux the server is made undumpable, so its own
//     environment cannot be read through /proc either; a worker exits when
//     its stdin closes.
//
// The server trusts a worker only as far as its code could go. Every frame of
// a job carries the job's nonce, and a worker that makes more helper calls
// than its code may, asks for a helper there is none of, or sends more than
// one call may carry breaks the protocol. The job's input lineage and file
// references never leave the server: a worker returns the code's results as
// the JSON the code returned, and the server decodes them against the input,
// checking the output and console caps, every file a result names (one of
// its input's, or one it stored), and the static data it hands back. A
// worker that breaks any of this fails its job with an engine fault and is
// never used again.
//
// What a worker is not: a privilege boundary. It runs as the server's user,
// with the server's view of the filesystem and network, so code that escaped
// the engine itself, which exposes no file, process or network API to a
// script, would not be contained by the process around it. The workers
// contain what the engine cannot stop from inside: a built-in that runs away
// with a core or the memory.
//
// This package starts processes, so it is kept apart from internal/jsrun,
// which must reach no process, network or file.
package jsworker
