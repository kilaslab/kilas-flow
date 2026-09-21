// Package runcode compiles and executes user-supplied Go for the Code node.
//
// User code never runs in the kilasflow process. Source is compiled with
// GOOS=wasip1, cached by source hash, and executed under wazero with no host
// functions of the sandbox's own — so the guest cannot reach the filesystem,
// the network, the environment, or this process's memory.
//
// # Capabilities
//
// A module's capability surface is exactly what its call's HostBinding
// installs, and nothing else: every execution gets its own runtime carrying
// WASI's standard streams, the two clocks, and no filesystem, environment or
// arguments. The Code node installs no binding at all, which is why it stays
// safe to hand to a tenant; a node pack is granted a host module built from
// the capabilities its manifest declares, which is what this seam exists for.
// Every host call is counted, and a module that spends its budget is stopped
// with ErrHostCallLimit.
//
// # Limits
//
// One call is bounded four ways — wall clock, linear memory, output bytes and
// host calls — and exceeding any of them fails that run with a named error
// (ErrTimeLimit, ErrMemoryLimit, ErrOutputLimit, ErrHostCallLimit) inside an
// ExecutionError whose Detail is the sentence a user reads. A limit failure is
// never the process: every module runs in a runtime this package closes.
//
// # Where the toolchain lives
//
// Compiling Go needs the full toolchain, roughly 270MB, which cannot ship
// inside the distroless single binary the product distributes. That was left
// open for a long time; it is settled here.
//
// The Compiler interface is the seam, and a deployment supplies whichever
// implementation it has:
//
//   - ToolchainCompiler, for a machine that already has `go` on its PATH —
//     development, and self-hosted installs that do not mind the image size.
//   - Nothing at all, which is the default for the distroless image. The Code
//     node is then visibly unavailable rather than silently broken: Available
//     reports false, the node catalogue says so, and the editor can grey the
//     node out *before* a workflow is saved.
//   - A compiler service reachable over this same interface, which is the
//     hosted answer. Nothing here needs to change to add one: Cache already
//     keys artifacts by source hash, so a service compiles each distinct body
//     once for the whole installation.
//
// What is deliberately *not* the answer is bundling the toolchain into the
// runtime image. It triples the image, puts a compiler on every machine that
// runs a workflow, and buys nothing a cache in front of one compiler does not
// buy more cheaply.
//
// The rule that follows from all three shapes: a user must never discover at
// run time that their deployment cannot compile. Availability is part of the
// node catalogue, so the editor knows before the workflow is saved.
//
// # The operator recipe
//
// A deployment that wants a toolchain provides one and says where it is. The
// toolchain is roughly 270MB and needs no packaging of its own: mount it into
// the container and point the server at its go command, either with an
// environment variable
//
//	docker run -v /path/to/go:/usr/local/go:ro -e KILASFLOW_CODE_GO_BINARY=/usr/local/go/bin/go ...
//
// or with the same setting in the configuration file, code.go_binary. Putting
// the toolchain's bin directory on the kilasflow process's PATH works just as
// well. The toolchain needs its standard library and nothing else; a build
// runs with the network disabled, so a Go installation without module
// downloads is sufficient.
//
// The Go toolchain is needed only for Code nodes. A node pack ships as
// WebAssembly that its author already compiled, so installing one compiles
// nothing.
//
// # Persistence
//
// Three things are cached under code.cache_dir, and each costs something
// different to lose:
//
//   - the compiled artifact, under artifacts/<RuntimeVersion>/, whose
//     replacement needs the Go toolchain — the one thing a deployment may not
//     have;
//   - wazero's translation of that artifact into machine code, under
//     translations/<RuntimeVersion>/, whose replacement needs only the
//     artifact and a moment of CPU;
//   - the toolchain's own build cache, under go-build/, which shortens the
//     rebuild that a changed source requires.
//
// Every directory is keyed on RuntimeVersion, so an artifact or a translation
// written against an older compilation or execution contract is never loaded:
// after an upgrade the first run rebuilds rather than executing yesterday's
// machine code against today's host. The default location is
// ./data/codecache, inside the data volume a container deployment already
// persists; code.cache_dir set to empty keeps everything in memory, which is
// what this package did before the key existed.
//
// A persisted translation is native machine code that this process loads and
// executes, so the cache directory is a trust boundary. It is created 0700
// with 0600 files and must stay writable only by the user kilasflow runs as;
// an operator who cannot arrange that should keep the caches in memory.
// code.cache_max_bytes bounds the artifact and translation directories
// together (translations evicted first, since they are the cheap half) and
// defaults to 2 GiB, because on a deployment with no toolchain an evicted
// artifact cannot be rebuilt at all.
package runcode
