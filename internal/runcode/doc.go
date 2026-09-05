// Package runcode compiles and executes user-supplied Go for the Code node.
//
// User code never runs in the kilasflow process. Source is compiled with
// GOOS=wasip1, cached by source hash, and executed under wazero with no host
// functions at all — so the guest cannot reach the filesystem, the network, the
// environment, or this process's memory.
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
package runcode
