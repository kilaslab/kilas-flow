// Package shippedcatalogue is the node catalogue a default KilasFlow install
// serves, assembled without a server.
//
// Three things need that catalogue and used to keep a private copy of how to
// build it: the test that holds the docs' stated size to the real one, the
// docs generator that writes the size, and the skills' example harness that
// validates a workflow against the nodes an installation really has. A copy of
// run()'s registration calls is the drift they exist to catch, so there is one,
// here, and a node added to run() is added to Register in the same change.
//
// The registry Register fills is the one run() holds right before directory
// packs load (packs.dir): the compiled nodes, the vector nodes, and the two
// embedded packs. An operator's own packs and sidecar nodes are theirs, not the
// product's, and are not counted.
package shippedcatalogue

import (
	"fmt"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/loadoptions"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/nodepack"
	"github.com/kilaslab/kilas-flow/internal/routing"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/webhook"
	"github.com/kilaslab/kilas-flow/nodes"
	"github.com/kilaslab/kilas-flow/packs/telegram"
	"github.com/kilaslab/kilas-flow/packs/waha"
)

// Register adds every node a default install serves to reg, in the order run()
// registers them.
//
// Only the definitions are wanted, so the executors, routes and option loaders
// the packs insist on are built here and thrown away: a pack refuses to
// register against an executor the server has not installed, and that
// refusal is part of what makes the catalogue what it is.
//
// The vector nodes are registered as available. A server without the pgvector
// extension registers the same definitions with a reason attached, so the
// catalogue's size does not depend on the database.
func Register(reg *node.Registry) error {
	if err := nodes.RegisterAll(reg); err != nil {
		return fmt.Errorf("register the compiled nodes: %w", err)
	}
	if err := nodes.RegisterVectorNodes(reg, ""); err != nil {
		return fmt.Errorf("register the vector nodes: %w", err)
	}

	policy := safehttp.DefaultPolicy()
	routes := routing.NewRegistry()
	executors := engine.NewRegistry()
	options := loadoptions.NewResolver(policy, 0)
	if err := nodes.RegisterRoutingExecutor(executors, policy, routes, reg); err != nil {
		return fmt.Errorf("register the declarative routing executor: %w", err)
	}
	deliveries := webhook.NewRegistry()
	if err := nodes.RegisterTriggerKinds(deliveries); err != nil {
		return fmt.Errorf("register the webhook trigger kinds: %w", err)
	}
	triggers := nodepack.NewTriggerRegistry()
	if err := executors.Register(nodepack.TriggerExecutorID, nodepack.NewTriggerExecutor(triggers, policy)); err != nil {
		return fmt.Errorf("register the pack trigger executor: %w", err)
	}
	if err := telegram.Register(reg, routes, executors, options); err != nil {
		return fmt.Errorf("register the Telegram node pack: %w", err)
	}
	if err := waha.Register(waha.Deps{
		Definitions: reg, Routes: routes, Triggers: triggers,
		Deliveries: deliveries, Lifecycles: webhook.NewLifecycleRegistry(),
		Executors: executors, Options: options,
	}); err != nil {
		return fmt.Errorf("register the WAHA node pack: %w", err)
	}

	return nil
}

// Size is how many node types a set of registrations holds, and how many
// type-and-version pairs: GET /api/v1/node-types lists one entry per pair, so
// a type served at two versions is one type and two pairs.
type Size struct {
	Types int
	Pairs int
}

// Counts is the catalogue's size, split by where each node was registered from.
type Counts struct {
	// Compiled is the nodes built into the binary (node.SourceBuiltin),
	// including the import placeholder for nodes KilasFlow does not implement.
	Compiled Size
	// Packs is the nodes from the declarative packs the binary embeds
	// (node.SourcePack).
	Packs Size
}

// Total is the whole catalogue. Types are never shared between the two sides: a
// pack may not claim the built-in namespace, so the sum is the distinct count.
func (counts Counts) Total() Size {
	return Size{
		Types: counts.Compiled.Types + counts.Packs.Types,
		Pairs: counts.Compiled.Pairs + counts.Packs.Pairs,
	}
}

// Count measures reg the way GET /api/v1/node-types lists it, split by the
// registry's own source tag. A sidecar entry is an operator's installation, not
// a shipped node, and belongs to neither side.
func Count(reg *node.Registry) Counts {
	types := map[node.Source]map[string]struct{}{
		node.SourceBuiltin: {},
		node.SourcePack:    {},
	}
	pairs := map[node.Source]int{}
	for _, definition := range reg.List() {
		seen, counted := types[definition.Source]
		if !counted {
			continue
		}
		seen[definition.Type] = struct{}{}
		pairs[definition.Source]++
	}

	return Counts{
		Compiled: Size{Types: len(types[node.SourceBuiltin]), Pairs: pairs[node.SourceBuiltin]},
		Packs:    Size{Types: len(types[node.SourcePack]), Pairs: pairs[node.SourcePack]},
	}
}

// RenderSentence is the one sentence both docs pages use to state the
// catalogue's size. It is a single line, so a page can hold it between two
// generated markers and a test can compare it byte for byte.
func RenderSentence(counts Counts) string {
	total := counts.Total()

	return fmt.Sprintf("A default install serves %d distinct node %s across %d type-and-version %s: "+
		"%s compiled into the binary, counting the `kilasflow.unsupported` import placeholder "+
		"for nodes KilasFlow does not implement, and %s from the declarative packs it ships with.",
		total.Types, plural(total.Types, "type", "types"),
		total.Pairs, plural(total.Pairs, "pair", "pairs"),
		describe(counts.Compiled), describe(counts.Packs))
}

// describe renders one side of the split: "59 types (64 pairs)".
func describe(size Size) string {
	return fmt.Sprintf("%d %s (%d %s)",
		size.Types, plural(size.Types, "type", "types"), size.Pairs, plural(size.Pairs, "pair", "pairs"))
}

func plural(count int, one, many string) string {
	if count == 1 {
		return one
	}

	return many
}
