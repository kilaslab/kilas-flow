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
//
// # Registration merges, it does not replace
//
// WAHA's `PUT /api/sessions/{session}` replaces a session's whole configuration
// and restarts it, so a trigger that sent one webhook would delete the
// customer's other webhooks, their proxy and their engine settings, and restart
// their live session. The trigger therefore reads the session, replaces only
// its own entry and writes the session back, from the description in
// `webhook-lifecycle.json` beside the packs.
package waha

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"

	"github.com/kilaslabs/kilas-flow/internal/loadoptions"
	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/nodepack"
	"github.com/kilaslabs/kilas-flow/internal/routing"
	"github.com/kilaslabs/kilas-flow/internal/webhook"
)

//go:embed pack-*.json webhook-lifecycle.json
var packs embed.FS

// MergeFile describes how this pack installs its delivery URL into a session
// that is already configured.
//
// A file of its own rather than a block in the trigger pack because the pack
// format refuses unknown fields: a merge cannot be expressed as a descriptor
// (`set` sends one fixed body, and WAHA's PUT replaces the session's whole
// configuration and restarts it), and adding fields to the generated format for
// one service's shape would put one service's shape in everybody's format.
const mergeFile = "webhook-lifecycle.json"

// NodeType is the action node this pack registers, at two versions.
const NodeType = "pack.waha"

// TriggerNodeType is the webhook trigger, also at two versions.
const TriggerNodeType = "pack.wahaTrigger"

// CredentialType is what every request the pack makes authenticates with.
const CredentialType = "wahaApi"

// Files are the generated packs, in the order they are registered.
var Files = []string{
	"pack-202409.json", "pack-202502.json",
	"pack-trigger-202409.json", "pack-trigger-202502.json",
}

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

// Deps is everything a pack registers into.
//
// Grouped rather than passed as six parameters because the point is that they
// arrive together: a definition without its routing, its event table, its
// delivery shape or its lifecycle hook is a node that registers cleanly and
// fails later.
type Deps struct {
	Definitions *node.Registry
	Routes      *routing.Registry
	Triggers    *nodepack.TriggerRegistry
	Deliveries  *webhook.Registry
	Lifecycles  *webhook.LifecycleRegistry
	Executors   nodepack.ExecutorSet
	Options     *loadoptions.Resolver
}

// Register installs every version of both node types.
func Register(deps Deps) error {
	decoded, err := Packs()
	if err != nil {
		return err
	}
	merge, err := mergeLifecycle()
	if err != nil {
		return err
	}
	for _, pack := range decoded {
		if err := nodepack.Register(deps.Definitions, deps.Routes, deps.Executors, deps.Options, pack); err != nil {
			return fmt.Errorf("register WAHA pack %s v%s: %w", pack.Type, pack.Version, err)
		}
		if pack.Trigger == nil {
			continue
		}
		if err := registerMerge(deps, pack, merge); err != nil {
			return fmt.Errorf("register WAHA trigger v%s: %w", pack.Version, err)
		}
		if err := nodepack.RegisterTrigger(deps.Triggers, deps.Deliveries, deps.Lifecycles, pack); err != nil {
			return fmt.Errorf("register WAHA trigger v%s: %w", pack.Version, err)
		}
	}
	return nil
}

// mergeLifecycle reads the merge description.
//
// Strict, like the pack format and for the same reason: a merge file naming a
// field this build does not implement must fail at startup rather than
// registering a lifecycle that quietly does something else.
func mergeLifecycle() (webhook.WebhookListLifecycle, error) {
	raw, err := packs.ReadFile(mergeFile)
	if err != nil {
		return webhook.WebhookListLifecycle{}, fmt.Errorf("read %s: %w", mergeFile, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var merge webhook.WebhookListLifecycle
	if err := decoder.Decode(&merge); err != nil {
		return webhook.WebhookListLifecycle{}, fmt.Errorf("%s: %w", mergeFile, err)
	}
	return merge, nil
}

// registerMerge binds the read-merge-write lifecycle in place of the trigger's
// descriptors.
//
// The trigger's own lifecycle block is cleared first, because
// nodepack.RegisterTrigger registers the descriptor form under the same ID and
// the registry keeps whichever arrived first: a descriptor `set` sends one
// fixed body, so leaving it in would restore exactly the behaviour this exists
// to remove. The ID and the gating parameter are the pack's — by the time this
// runs the node definition already carries the ID — and only the requests
// behind them are replaced.
//
// Both versions declare one ID and one merge: the merge is the same document
// for both, so the first registration is the one that counts.
func registerMerge(deps Deps, pack *nodepack.Pack, merge webhook.WebhookListLifecycle) error {
	declared := pack.Trigger.Lifecycle
	if declared == nil {
		return nil
	}
	notice := pack.Trigger.Notice
	pack.Trigger.Lifecycle = nil
	if _, registered := deps.Lifecycles.Lookup(declared.ID); registered {
		return nil
	}
	gated := webhook.GatedLifecycle{
		EnabledParameter: declared.EnabledParameter,
		Lifecycle:        merge,
		Notice:           notice,
	}
	if err := deps.Lifecycles.Register(declared.ID, gated); err != nil {
		return fmt.Errorf("register webhook lifecycle %q: %w", declared.ID, err)
	}
	return nil
}
