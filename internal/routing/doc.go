// Package routing interprets declarative node metadata as an HTTP request.
//
// n8n has two ways to write a node. A programmatic node ships an `execute()`
// function; a declarative node ships only metadata — `requestDefaults` on the
// description and a `routing` object on each property saying how that property
// becomes part of a request. `@devlikeapro/n8n-nodes-waha`'s action node is
// entirely the second kind: 124 OpenAPI-derived operations and no `execute()`
// at all. That is the single fact that makes a WAHA pack reachable without a
// JavaScript runtime, and this package is the interpreter that cashes it in.
//
// # Where the metadata lives
//
// Routing metadata is held here, in a Registry keyed by `{node type, version}`,
// and deliberately *not* on `node.Definition`.
//
// `node.Definition` is served by the node-types API and is a published
// contract: adding a `routing` field would put a node's outbound request
// internals — URLs, header names, credential template references — into a JSON
// body handed to every browser that opens the editor, to describe something the
// editor has no use for. A pack therefore ships two things that travel
// together: a definition, which is what the editor sees, and a routing
// description, which is what this interpreter sees. The pairing is checked at
// registration, so a definition bound to the routing executor with no routing
// description fails at startup rather than at run time.
//
// # What is implemented, and what is refused
//
// The subset is the subset generated packs and the Telegram node actually emit:
// request defaults, `routing.request`, `routing.send`, the `rootProperty`,
// `setKeyValue` and `limit` post-receive actions, and offset pagination.
//
// n8n's `preSend` and the function form of `postReceive` are JavaScript
// closures and have no representation in data. A pack carrying one is rejected
// when it is registered — never ignored at run time, because a request that
// quietly skips the step that was going to sign it is worse than a pack that
// will not load. Decoding uses DisallowUnknownFields for the same reason: an
// unimplemented routing feature must be a loud failure, not a silent omission.
//
// # Security posture
//
// Every request this interpreter makes goes through `internal/safehttp` — the
// URL is checked before the dial, the resolved address is checked again at dial
// time and on every redirect hop, and the response is bounded — and through the
// same credential path the hand-written HTTP node uses,
// `engine.Request.Authenticate`, which checks the credential's declared type
// and the target host against the credential's allowed domains. A JavaScript
// sidecar running third-party `fetch` calls could not have been held to any of
// that; interpreting the metadata in Go is what keeps it.
//
// Routing templates are resolved by `internal/expression` with two extra roots,
// `$parameter` and `$credentials`, rather than by a second private evaluator.
// `$credentials` is populated from `credentials.Split`, so it carries the
// credential type's non-secret fields only — a base URL, never a token.
package routing
