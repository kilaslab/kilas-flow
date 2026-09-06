// Package sdk is the author-facing Go module for native KilasFlow node packs.
//
// This is the guest side of the WebAssembly pack story (FEAT-48hreg): a pack
// author imports this module, writes a run function over workflow items, and
// builds a wasip1 module the host executes under wazero. It is deliberately
// small — one contract, no dependencies beyond the standard library — because
// every line here ships inside a third party's module.
//
// # The two SDKs
//
// Two unrelated things in this repository share the word SDK and must not be
// confused:
//
//   - pkg/sdk (this module) is the guest-side Go module a WASM pack author
//     imports. Audience: node authors. Runtime: inside the sandbox.
//   - sdk/ (@kilasflow/sdk on npm) is the TypeScript host SDK a host
//     application installs to drive KilasFlow's API. Audience: embedding
//     developers. Runtime: outside the server.
//
// Neither is a version of the other. See docs/guides/community-nodes.md.
//
// # Contract
//
// The pack keeps the Code node's batch contract (internal/runcode): everything
// the node needs arrives as one JSON document on stdin, one document leaves on
// stdout. The module needs nothing beyond WASI's standard streams, which is
// what keeps the capability surface auditable — a pack that declares no host
// capabilities is granted none, and the host side of every capability is
// policed by the host, never by anything the guest says.
//
// Stdin: [{"json": {...}}, ...]
// Stdout success: {"items": [{"json": {...}}, ...]}
// Stdout failure: {"error": "message"} (with a non-zero exit)
//
// # Building
//
// The host builds and runs the module; the author only produces it:
//
//	GOOS=wasip1 GOARCH=wasm CGO_ENABLED=0 go build -o pack.wasm ./...
//
// An example pack proving the path lives under example/echo.
//
// # Versioning
//
// Version is the contract version. The host refuses artifacts built against a
// different contract rather than running them against today's wrapper, so a
// bump here invalidates every cached artifact instead of silently changing
// what a pack means.
package sdk
