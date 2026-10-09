package shippedcatalogue_test

import (
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/shippedcatalogue"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// definition is the smallest node the registry accepts, at one version.
func definition(nodeType string, version int) node.Definition {
	return node.Definition{
		Type: nodeType, Version: workflow.V(version), DisplayName: nodeType,
		Category: "Test", Group: []node.NodeGroup{node.GroupTransform}, ExecutorID: "test",
	}
}

// A type served at two versions is one type and two pairs, and a pack's entry is
// counted on the packs' side, not the compiled one. The split is the registry's
// own Source tag, so a count can never drift from how a node was registered.
func TestCountSplitsTypesAndPairsBySource(t *testing.T) {
	registry := node.NewRegistry()
	for _, entry := range []struct {
		nodeType string
		version  int
	}{{"kilasflow.alpha", 1}, {"kilasflow.alpha", 2}, {"kilasflow.beta", 1}} {
		if err := registry.Register(definition(entry.nodeType, entry.version)); err != nil {
			t.Fatalf("register %s: %v", entry.nodeType, err)
		}
	}
	if err := registry.RegisterFrom(node.SourcePack, definition("acme.gamma", 1)); err != nil {
		t.Fatalf("register the pack node: %v", err)
	}

	got := shippedcatalogue.Count(registry)
	want := shippedcatalogue.Counts{
		Compiled: shippedcatalogue.Size{Types: 2, Pairs: 3},
		Packs:    shippedcatalogue.Size{Types: 1, Pairs: 1},
	}
	if got != want {
		t.Fatalf("Count() = %+v, want %+v", got, want)
	}
	if total := got.Total(); total != (shippedcatalogue.Size{Types: 3, Pairs: 4}) {
		t.Fatalf("Total() = %+v, want 3 types across 4 pairs", total)
	}
}

// The catalogue a default install serves is more than nodes.RegisterAll: the
// vector nodes and the two embedded packs are registered by run() as well, and
// a consumer that stopped at RegisterAll would believe fewer nodes exist.
func TestRegisterHoldsTheCompiledNodesTheVectorNodesAndTheEmbeddedPacks(t *testing.T) {
	registry := node.NewRegistry()
	if err := shippedcatalogue.Register(registry); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	served := map[string]node.Source{}
	for _, entry := range registry.List() {
		served[entry.Type] = entry.Source
	}
	for nodeType, source := range map[string]node.Source{
		"kilasflow.manual":      node.SourceBuiltin,
		"kilasflow.agent":       node.SourceBuiltin,
		"kilasflow.vectorStore": node.SourceBuiltin,
		"kilasflow.unsupported": node.SourceBuiltin,
	} {
		if got, ok := served[nodeType]; !ok || got != source {
			t.Errorf("%s: served = %v from %q, want a %q node", nodeType, ok, got, source)
		}
	}

	counts := shippedcatalogue.Count(registry)
	if counts.Compiled.Types == 0 || counts.Packs.Types == 0 {
		t.Fatalf("Count() = %+v, want compiled and pack nodes", counts)
	}
	for _, size := range []shippedcatalogue.Size{counts.Compiled, counts.Packs} {
		if size.Pairs < size.Types {
			t.Errorf("a side with %d types has %d pairs, which cannot be fewer", size.Types, size.Pairs)
		}
	}
	if total, pairs := counts.Total(), len(registry.List()); total.Pairs != pairs {
		t.Errorf("Total().Pairs = %d, but the registry lists %d type-and-version pairs", total.Pairs, pairs)
	}

	// Registered twice into one registry is a refusal, not a silent double count.
	if err := shippedcatalogue.Register(registry); err == nil {
		t.Error("Register() into a registry that already holds the catalogue returned no error")
	}
}

func TestRenderSentenceIsTheOneCanonicalStatementOfTheSize(t *testing.T) {
	sentence := shippedcatalogue.RenderSentence(shippedcatalogue.Counts{
		Compiled: shippedcatalogue.Size{Types: 59, Pairs: 64},
		Packs:    shippedcatalogue.Size{Types: 3, Pairs: 5},
	})

	for _, fragment := range []string{
		"62 distinct node types",
		"69 type-and-version pairs",
		"59 types (64 pairs) compiled into the binary",
		"3 types (5 pairs) from the declarative packs",
	} {
		if !strings.Contains(sentence, fragment) {
			t.Errorf("RenderSentence() = %q, want it to hold %q", sentence, fragment)
		}
	}
	if strings.Contains(sentence, "\n") {
		t.Errorf("RenderSentence() = %q, want one line so a page can splice it in place", sentence)
	}
	if !strings.HasSuffix(sentence, ".") {
		t.Errorf("RenderSentence() = %q, want a finished sentence", sentence)
	}
}

// One pack type is "1 type", and the sentence says so; "1 types" is the kind of
// slip a generated sentence is meant to remove, not introduce.
func TestRenderSentenceAgreesInNumber(t *testing.T) {
	sentence := shippedcatalogue.RenderSentence(shippedcatalogue.Counts{
		Compiled: shippedcatalogue.Size{Types: 1, Pairs: 1},
		Packs:    shippedcatalogue.Size{Types: 1, Pairs: 1},
	})
	for _, fragment := range []string{"2 distinct node types", "2 type-and-version pairs", "1 type (1 pair) compiled"} {
		if !strings.Contains(sentence, fragment) {
			t.Errorf("RenderSentence() = %q, want it to hold %q", sentence, fragment)
		}
	}
	if strings.Contains(sentence, "1 types") || strings.Contains(sentence, "1 pairs") {
		t.Errorf("RenderSentence() = %q, want singular agreement", sentence)
	}
}
