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
// The host writes one invocation envelope to stdin and reads one document from
// stdout:
//
//	{"abi":"v1","node":{"type":"…","version":1,"name":"…"},
//	 "parameters":{…},"items":[{"json":{…},"binary":{…}}]}
//	→ {"items":[{"json":{…}}]}                  (MainCall, one port)
//	→ {"outputs":[[{"json":{…}}],[…]]}          (MainPorts, one array per port)
//	→ {"error":"message"}                       (any failure, non-zero exit)
//
// Items carry payload references rather than payloads, exactly as they do
// inside the engine.
//
// A pack written against the older batch contract — a bare item array on
// stdin, items back — still works: Handle and Main accept the envelope too, so
// such a pack installs and runs as a module pack, but it cannot see parameters
// and cannot call capabilities. example/echo is that shape, and it is a real
// capability-free pack.
//
// # Capabilities
//
// A pack reaches the host only through the functions its manifest's declared
// capabilities grant, in the host module named by HostModule:
//
//   - http (sdk.HTTP): outbound HTTP through the deployment's SSRF policy,
//     with a credential the node attached applied by the host.
//   - credentials (sdk.CredentialField): the non-secret fields of a credential
//     the node attached. Secret fields never cross the boundary; naming a
//     credential in HTTPRequest.Credential has the host apply it instead.
//   - binary.read (sdk.ReadBinary): payloads the input items carry, or ones
//     this run wrote.
//   - binary.write (sdk.WriteBinary): store a payload and return the reference
//     an output item may carry.
//
// A pack that declares none imports no host function at all: the linker drops
// the unreferenced declarations, and the host instantiates no host module, so
// the capability surface is what the module actually reaches rather than what
// it promised. Every call is policed on the host's side — the URL check, the
// credential scope, the pointer bounds and the per-call limits are the host's,
// never anything the guest says.
//
// Calls fail with a *HostError, which carries the host's own message and
// answers errors.Is for ErrDeniedError, ErrBlockedError, ErrNotFoundError and
// the rest, so a pack can branch on what happened.
//
// # Limits
//
// A run is bounded on the host's side: wall clock, linear memory, output bytes
// and the number of host calls. Exceeding one fails that node run with a named
// error and never the process; the limits themselves come from the pack's
// manifest, up to the deployment's ceilings.
//
// # Testing a pack without a sandbox
//
// The capability functions work off the guest target too. Install a fake host
// and call the pack's run function directly:
//
//	sdk.SetHost(fake)
//	output, exit := sdk.HandleCall(stdin, run)
//
// SetHost exists only in a non-wasip1 build; a pack built for wasip1 always
// calls the real host. Nothing about the sandbox is proven this way, which is
// why the host's own tests drive real modules.
//
// # Building
//
// The host builds and runs the module; the author only produces it:
//
//	GOOS=wasip1 GOARCH=wasm CGO_ENABLED=0 go build -o pack.wasm ./...
//
// Example packs proving the path live under example/echo (capability-free, the
// legacy batch contract) and example/fetch (http and credentials).
//
// # Versioning
//
// Version is the legacy batch contract's version: the host keys cached
// artifacts on it, so a bump invalidates every cached artifact rather than
// silently changing what a pack means. ABIVersion is the invocation envelope's
// and the host module's, declared by a pack's manifest and checked when the
// pack is loaded. The two move independently because the batch contract is
// byte-identical to what it always was.
package sdk
