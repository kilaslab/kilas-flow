package sidecarnode_test

import (
	"bufio"
	"context"
	"encoding/json"

	"net"
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/credentials"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/sidecarnode"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
	"github.com/kilaslab/kilas-flow/sidecar"
	"github.com/kilaslab/kilas-flow/sidecar/sidecartest"
)

// describeSpawn answers a describe run with a catalogue written by the test.
//
// It speaks the wire protocol directly (NDJSON over the socket, the request id
// echoed back) rather than going through the runner, so these tests can pin the
// load policy — which failures exclude a node and which refuse the boot —
// without a Node process or a package on disk.
func describeSpawn(catalogue string) sidecar.SpawnFunc {
	return func(ctx context.Context, tenant, _ string) (*sidecar.Child, error) {
		host, child := net.Pipe()
		go func() {
			defer child.Close()
			reader := bufio.NewReader(child)
			for {
				line, err := reader.ReadString('\n')
				if err != nil {
					return
				}
				var frame struct {
					Type string `json:"type"`
					ID   string `json:"id"`
				}
				if err := json.Unmarshal([]byte(line), &frame); err != nil {
					return
				}
				answer, err := json.Marshal(map[string]any{
					"type": "result", "id": frame.ID, "catalogue": json.RawMessage(catalogue),
				})
				if err != nil {
					return
				}
				if _, err := child.Write(append(answer, '\n')); err != nil {
					return
				}
			}
		}()
		return &sidecar.Child{Conn: host, Kill: func() error {
			_ = host.Close()
			_ = child.Close()
			return nil
		}}, nil
	}
}

func loadLimits() sidecar.Limits {
	limits := sidecar.DefaultLimits()
	limits.Timeout = 10 * time.Second
	limits.SpawnTimeout = 10 * time.Second
	return limits
}

// catalogueJSON renders a catalogue the way the runner would.
func catalogueJSON(t *testing.T, packages ...sidecarnode.PackageInfo) string {
	t.Helper()
	encoded, err := json.Marshal(map[string]any{"nodeVersion": "v24.16.0", "packages": packages})
	if err != nil {
		t.Fatalf("marshalling the catalogue: %v", err)
	}
	return string(encoded)
}

func loadFrom(t *testing.T, catalogue string, registry *node.Registry, credentialRegistry *credentials.Registry) (*sidecarnode.Index, error) {
	t.Helper()
	if registry == nil {
		registry = node.NewRegistry()
	}
	if credentialRegistry == nil {
		credentialRegistry = credentials.NewRegistry()
	}
	return sidecarnode.Load(context.Background(), sidecarnode.LoadDeps{
		Spawn: describeSpawn(catalogue), Limits: loadLimits(),
		Definitions: registry, Credentials: credentialRegistry,
	})
}

// TestLoadTreatsPerFileFailureAsExclusionNotRefusal is the failure policy: one
// broken node in one package must not take the boot down.
func TestLoadTreatsPerFileFailureAsExclusionNotRefusal(t *testing.T) {
	pkg := greetPackage()
	pkg.Errors = []sidecarnode.PackageError{{
		Package: pkg.Name, File: "dist/nodes/Bad/Bad.node.js", Code: "missing-module",
		Message: "this file needs the module \"n8n-workflow\", which is not installed", Severity: "file",
	}}

	registry := node.NewRegistry()
	index, err := loadFrom(t, catalogueJSON(t, pkg), registry, nil)
	if err != nil {
		t.Fatalf("Load() error = %v, want the per-file failure excluded rather than fatal", err)
	}
	if got, want := index.Len(), 1; got != want {
		t.Fatalf("registered nodes = %d, want %d", got, want)
	}
	if _, found := registry.Get("sidecar.kf-fixture-nodes.fixtureGreet", workflow.V(1)); !found {
		t.Error("the package's other node was not registered")
	}
	exclusions := index.Exclusions()
	if len(exclusions) != 1 {
		t.Fatalf("exclusions = %+v, want one", exclusions)
	}
	if !strings.Contains(exclusions[0].Reason, "missing-module") || exclusions[0].File != "dist/nodes/Bad/Bad.node.js" {
		t.Errorf("exclusion = %+v, want the runner's own code and file", exclusions[0])
	}
}

// TestLoadRefusesAPackageWithAFatalError is the other side of that policy: a
// package-level failure means nothing in it can be trusted.
func TestLoadRefusesAPackageWithAFatalError(t *testing.T) {
	pkg := greetPackage()
	pkg.Errors = []sidecarnode.PackageError{{
		Package: pkg.Name, File: "package.json", Code: "manifest-api-version",
		Message: "n8n.n8nNodesApiVersion is 2, and this sidecar loads version 1 only", Severity: "fatal",
	}}

	index, err := loadFrom(t, catalogueJSON(t, pkg), nil, nil)
	if err == nil {
		t.Fatalf("Load() error = nil, want the package refused")
	}
	if index != nil {
		t.Errorf("Load() returned an index alongside its error: %+v", index)
	}
	if !strings.Contains(err.Error(), pkg.Name) || !strings.Contains(err.Error(), "manifest-api-version") {
		t.Errorf("error = %v, want it to name the package and the defect", err)
	}
}

// TestLoadRefusesTheSameTypeFromTwoPackages is the collision only a human can
// settle: two packages claiming one node type. The names slug to the same
// identifier, which is exactly how it happens with a scoped and an unscoped
// publication of the same package.
func TestLoadRefusesTheSameTypeFromTwoPackages(t *testing.T) {
	first, second := greetPackage(), greetPackage()
	first.Name, second.Name = "@scope/kf-fixture-nodes", "scope/kf-fixture-nodes"
	// The second package is a distinct publication with no credential of its
	// own, so the only thing that collides is the node type.
	second.Credentials = nil
	second.Nodes[0].Description["credentials"] = []any{}

	_, err := loadFrom(t, catalogueJSON(t, first, second), nil, nil)
	if err == nil {
		t.Fatalf("Load() error = nil, want the duplicate type refused")
	}
	for _, want := range []string{first.Name, second.Name, "sidecar.scope-kf-fixture-nodes.fixtureGreet"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to name %q", err, want)
		}
	}
}

// TestDuplicateCredentialTypeIsRefusedNamingBothPackages is the same collision
// one level down: a credential type two packages both declare.
func TestDuplicateCredentialTypeIsRefusedNamingBothPackages(t *testing.T) {
	first, second := greetPackage(), greetPackage()
	first.Name, second.Name = "@scope/one", "scope/two"

	_, err := loadFrom(t, catalogueJSON(t, first, second), nil, nil)
	if err == nil {
		t.Fatalf("Load() error = nil, want the duplicate credential type refused")
	}
	for _, want := range []string{first.Name, second.Name, "fixtureApi"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to name %q", err, want)
		}
	}
}

// TestLoadRefusesATypeTheCatalogueAlreadyHas is the third collision: a sidecar
// package claiming a type this deployment already serves.
func TestLoadRefusesATypeTheCatalogueAlreadyHas(t *testing.T) {
	registry := node.NewRegistry()
	existing := node.Definition{
		Type: "sidecar.kf-fixture-nodes.fixtureGreet", Version: workflow.V(1),
		DisplayName: "Already here", Category: "Community", ExecutorID: "test.executor",
		Group:   []node.NodeGroup{node.GroupTransform},
		Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
	}
	if err := registry.RegisterFrom(node.SourcePack, existing); err != nil {
		t.Fatalf("RegisterFrom() error = %v", err)
	}

	_, err := loadFrom(t, catalogueJSON(t, greetPackage()), registry, nil)
	if err == nil {
		t.Fatalf("Load() error = nil, want the collision refused")
	}
	if !strings.Contains(err.Error(), "pack") {
		t.Errorf("error = %v, want it to name the node that already holds the type", err)
	}
}

// TestLoadRegistersSidecarSourceAndCredentialTypes is the criterion itself: an
// operator-installed package's nodes appear in the catalogue tagged sidecar,
// beside built-in and pack entries, with its credential types registered.
//
// It runs the real runner in a real Node process against the fixture packages
// and skips with a named reason without Node (KILASFLOW_TEST_REQUIRE_NODE=1
// turns that skip into a failure, so CI cannot skip silently).
func TestLoadRegistersSidecarSourceAndCredentialTypes(t *testing.T) {
	nodePath := sidecartest.Node(t)
	runnerPath, err := sidecar.ExtractRunner(t.TempDir())
	if err != nil {
		t.Fatalf("ExtractRunner() error = %v", err)
	}
	spawn := sidecar.RunnerSpawn(sidecar.RunnerConfig{
		NodePath: nodePath, RunnerPath: runnerPath,
		PackagesDir: sidecartest.PackagesDir(t), Packages: []string{"kf-fixture-nodes"},
		RuntimeDir: t.TempDir(),
	})

	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	pack := node.Definition{
		Type: "pack.example", Version: workflow.V(1), DisplayName: "Pack Example", Category: "Pack",
		ExecutorID: "pack.executor", Group: []node.NodeGroup{node.GroupTransform},
		Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
	}
	if err := registry.RegisterFrom(node.SourcePack, pack); err != nil {
		t.Fatalf("RegisterFrom() error = %v", err)
	}
	credentialRegistry := credentials.NewRegistry()

	index, err := sidecarnode.Load(context.Background(), sidecarnode.LoadDeps{
		Spawn: spawn, Limits: loadLimits(), Definitions: registry, Credentials: credentialRegistry,
		SharedSettings: nodes.SidecarSharedSettings(),
	})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(index.Exclusions()) != 0 {
		t.Errorf("exclusions = %+v, want none for the fixture package", index.Exclusions())
	}
	if got, want := index.Len(), 2; got != want {
		t.Fatalf("registered sidecar nodes = %d, want %d", got, want)
	}

	greet, found := index.Get("sidecar.kf-fixture-nodes.fixtureGreet", workflow.V(1))
	if !found {
		t.Fatalf("the greet node is not in the index")
	}
	if got, want := greet.Name, "fixtureGreet"; got != want {
		t.Errorf("dispatch name = %q, want %q", got, want)
	}
	if greet.NodeVersion != 1 {
		t.Errorf("dispatch version = %v, want 1", greet.NodeVersion)
	}
	if got, want := greet.HiddenDefaults["hiddenValue"], "kept-out-of-the-editor"; got != want {
		t.Errorf("hidden default = %#v, want %#v", got, want)
	}
	if len(greet.CredentialTypes) != 1 || greet.CredentialTypes[0] != "fixtureApi" {
		t.Errorf("credential types = %v, want fixtureApi", greet.CredentialTypes)
	}

	sources := map[string]int{}
	for _, definition := range registry.List() {
		sources[string(definition.Source)]++
	}
	for _, want := range []string{string(node.SourceBuiltin), string(node.SourcePack), string(node.SourceSidecar)} {
		if sources[want] == 0 {
			t.Errorf("the catalogue holds no %s node: %v", want, sources)
		}
	}
	for _, definition := range registry.List() {
		if strings.HasPrefix(definition.Type, "sidecar.") {
			if definition.Source != node.SourceSidecar {
				t.Errorf("%s is tagged %q, want sidecar", definition.Type, definition.Source)
			}
			if definition.ExecutorID != sidecarnode.ExecutorID {
				t.Errorf("%s is bound to %q, want %q", definition.Type, definition.ExecutorID, sidecarnode.ExecutorID)
			}
			// The shared settings are what give the editor Continue on Fail,
			// Retry and Timeout on a community node.
			if len(definition.SharedSettings) == 0 {
				t.Errorf("%s carries no shared settings", definition.Type)
			}
		}
	}

	credentialType, found := credentialRegistry.Get("fixtureApi")
	if !found {
		t.Fatalf("the package's credential type was not registered")
	}
	if len(credentialType.Secrets) != len(credentialType.Properties) {
		t.Errorf("secrets = %v over %d fields, want every field", credentialType.Secrets, len(credentialType.Properties))
	}
	if credentialType.DisplayName != "Fixture API" {
		t.Errorf("credential display name = %q, want Fixture API", credentialType.DisplayName)
	}
}

// TestLoadFailsWithoutASidecar is the deployment that declined it: the load
// fails with the install diagnostic rather than registering nothing quietly.
func TestLoadFailsWithoutASidecar(t *testing.T) {
	_, err := sidecarnode.Load(context.Background(), sidecarnode.LoadDeps{
		Definitions: node.NewRegistry(), Credentials: credentials.NewRegistry(),
	})
	if err == nil {
		t.Fatal("Load() error = nil, want the missing sidecar reported")
	}
	if !strings.Contains(err.Error(), "sidecar") {
		t.Errorf("error = %v, want it to name the missing sidecar", err)
	}
}

// TestLoadRefusesWithoutCatalogues keeps a composition mistake from becoming a
// nil-map panic three calls later.
func TestLoadRefusesWithoutCatalogues(t *testing.T) {
	for name, deps := range map[string]sidecarnode.LoadDeps{
		"no node catalogue":       {Credentials: credentials.NewRegistry()},
		"no credential catalogue": {Definitions: node.NewRegistry()},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := sidecarnode.Load(context.Background(), deps); err == nil {
				t.Errorf("Load() error = nil, want a refusal naming the missing catalogue")
			}
		})
	}
}

// TestIndexGetIsExact pins the lookup the executor depends on: the engine has
// already resolved the document's version, so a near miss must not dispatch.
func TestIndexGetIsExact(t *testing.T) {
	converted, _, _, err := sidecarnode.Convert(greetPackage())
	if err != nil {
		t.Fatalf("Convert() error = %v", err)
	}
	index := sidecarnode.NewIndex()
	index.Add(converted)
	if _, found := index.Get("sidecar.kf-fixture-nodes.fixtureGreet", workflow.V(1)); !found {
		t.Error("Get() did not find the registered version")
	}
	if _, found := index.Get("sidecar.kf-fixture-nodes.fixtureGreet", workflow.V(2)); found {
		t.Error("Get() resolved a version that was never registered")
	}
	if _, found := index.Get("sidecar.other.fixtureGreet", workflow.V(1)); found {
		t.Error("Get() resolved a type that was never registered")
	}
	if got, want := index.Len(), 1; got != want {
		t.Errorf("Len() = %d, want %d", got, want)
	}
}
