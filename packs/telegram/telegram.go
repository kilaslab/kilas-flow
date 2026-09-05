// Package telegram registers the Telegram action node.
//
// Unlike WAHA's, this pack is hand-written. Telegram publishes its API as HTML
// documentation rather than an OpenAPI document, and the community-maintained
// specs that do exist are unvetted third-party artifacts — for twenty-three
// operations with stable parameter names, metadata reviewed once is cheaper and
// safer than importing a conversion of somebody else's transcription.
//
// Hand-written, but still *declarative*: it runs on the same routing
// interpreter the generated packs do, so it inherits the egress policy, the
// credential host scope and the multipart upload path rather than growing its
// own executor. The one thing it does not do is what the trigger does — it
// makes no long-lived connection and holds no state — which is why the trigger
// stayed Go and this did not.
package telegram

import (
	_ "embed"
	"fmt"

	"github.com/kilaslabs/kilas-flow/internal/loadoptions"
	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/nodepack"
	"github.com/kilaslabs/kilas-flow/internal/routing"
)

//go:embed pack.json
var packJSON []byte

// NodeType is the action node this pack registers.
const NodeType = "pack.telegram"

// CredentialType is what every request it makes authenticates with. It is the
// same type the trigger uses, so one bot needs one credential.
const CredentialType = "telegramApi"

// Pack decodes the embedded pack.
func Pack() (*nodepack.Pack, error) {
	pack, err := nodepack.Decode(packJSON)
	if err != nil {
		return nil, fmt.Errorf("telegram pack: %w", err)
	}
	return pack, nil
}

// Register installs the node.
func Register(
	definitions *node.Registry,
	routes *routing.Registry,
	executors nodepack.ExecutorSet,
	options *loadoptions.Resolver,
) error {
	pack, err := Pack()
	if err != nil {
		return err
	}
	return nodepack.Register(definitions, routes, executors, options, pack)
}
