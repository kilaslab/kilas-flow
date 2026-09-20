// Package cli implements the `kilasflow <verb>` command surface.
//
// The CLI is a client of the HTTP API: it speaks to a server over HTTP, local
// or remote, and it therefore MUST NOT import internal/api outside _test.go
// files. Importing the server would make the CLI unable to talk to anything
// but its own process, and it would drag the API and the embedded SPA into the
// CLI's test binary.
//
// # The one sanctioned exception
//
// `pack validate` runs the real loader, internal/nodepack, because a pack is
// only validated against the loader that will run it — a second, lighter
// interpretation would accept manifests the server then refuses, which is the
// opposite of what a validation verb is for. That import is the package's only
// non-test dependency outside itself, and it does reach the engine and the
// database layer transitively. The cost is accepted deliberately: `go test
// ./internal/cli/...` links the database driver and the embedded migrations even
// though no CLI code path opens a database, so a break in that layer fails this
// package's build too.
//
// # Coexistence with the server
//
// The binary keeps its original meaning. `kilasflow` with no subcommand, with
// only flags, or with the explicit `serve` verb starts the server, because the
// container entrypoint and every Compose file depend on that. Run reports
// handled=false for those invocations so cmd/kilasflow can carry on into its
// own flag parsing; every other invocation belongs to this package.
//
// # Output contract
//
// Every verb prints exactly one JSON envelope on stdout when stdout is not a
// terminal or --json is passed, and human-readable text otherwise. Exit codes
// are the machine-readable half of the contract: see ExitOK..ExitNotReady and
// exitForStatus.
package cli
