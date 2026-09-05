// Package waha registers the generated WAHA node pack.
//
// The pack is produced by `cmd/nodepackgen` from WAHA's own MIT OpenAPI
// document, vendored in `third_party/waha`. Nothing here is hand-written: the
// operations, their parameters and their requests are all in the JSON files
// beside this one, and the routing interpreter executes them.
//
// # Two versions, generated independently
//
// The n8n package this has to stay compatible with publishes `202409` and
// `202502`, and they are not additive — operations move and parameters change
// between them. Both are generated from their own spec and registered as
// distinct versions of one node type, which the registry keys on `{type,
// version}` already. A workflow imported at `202409` keeps resolving against
// the `202409` definition for the life of the process.
//
// # The node type is `pack.waha`
//
// Not `kilasflow.waha`: `node.BuiltinPrefix` reserves that namespace for nodes
// compiled into this binary, and a pack registering into it could shadow — or
// be mistaken for — one this project ships. Not
// `@devlikeapro/n8n-nodes-waha.WAHA` either; the importer maps that foreign
// string onto this one and leaves the original visible in the import
// diagnostics, which is where a reader wants it.
package waha

import (
	"embed"
	"fmt"

	"github.com/kilaslabs/kilas-flow/internal/loadoptions"
	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/nodepack"
	"github.com/kilaslabs/kilas-flow/internal/routing"
)

//go:embed pack-*.json
var packs embed.FS

// NodeType is the one node type this pack registers, at two versions.
const NodeType = "pack.waha"

// CredentialType is what every request the pack makes authenticates with.
const CredentialType = "wahaApi"

// Files are the generated packs, in the order they are registered.
var Files = []string{"pack-202409.json", "pack-202502.json"}

// Packs decodes the embedded pack files.
func Packs() ([]*nodepack.Pack, error) {
	decoded := make([]*nodepack.Pack, 0, len(Files))
	for _, name := range Files {
		raw, err := packs.ReadFile(name)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		pack, err := nodepack.Decode(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		decoded = append(decoded, pack)
	}
	return decoded, nil
}

// Register installs both versions.
func Register(definitions *node.Registry, routes *routing.Registry, executors nodepack.ExecutorSet, options *loadoptions.Resolver) error {
	decoded, err := Packs()
	if err != nil {
		return err
	}
	for _, pack := range decoded {
		if err := nodepack.Register(definitions, routes, executors, options, pack); err != nil {
			return fmt.Errorf("register WAHA pack v%s: %w", pack.Version, err)
		}
	}
	return nil
}
