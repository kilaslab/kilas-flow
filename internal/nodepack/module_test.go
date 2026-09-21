package nodepack

import (
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/wasmpack"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	sdk "github.com/kilaslab/kilas-flow/pkg/sdk"
)

// modulePack is the smallest valid module pack, with the digest of the bytes a
// test hands the loader.
func modulePack(t *testing.T, module []byte, mutate func(*Pack)) *Pack {
	t.Helper()
	pack := &Pack{
		Type: "pack.module", Version: workflow.V(1), DisplayName: "Module", Category: "Transform",
		Module: &Module{
			File: "module.wasm", SHA256: digestOf(module), ABI: sdk.ABIVersion,
			Capabilities: []string{"http"},
			Credentials:  []ModuleCredential{{Type: "httpHeaderAuth", Required: true}},
			Limits:       &ModuleLimits{TimeoutSeconds: 5, MaxHostCalls: 7},
		},
	}
	if mutate != nil {
		mutate(pack)
	}
	return pack
}

// A module pack loads into a node the engine can run: it carries the pack
// executor, the ports its manifest declared and the credentials it may use.
func TestAModulePackDecodesAndRegistersTaggedPack(t *testing.T) {
	pack := modulePack(t, []byte("wasm-bytes"), func(pack *Pack) {
		pack.Module.Outputs = []ModuleOutput{{Name: "main"}, {Name: "extra", DisplayName: "Extra"}}
	})
	definition, description, err := Load(pack)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if description != nil {
		t.Errorf("a module pack produced a routing description, want none: it makes its own requests")
	}
	if definition.ExecutorID != wasmpack.ExecutorID {
		t.Errorf("ExecutorID = %q, want %q", definition.ExecutorID, wasmpack.ExecutorID)
	}
	if len(definition.Outputs) != 2 || definition.Outputs[0].Name != "main" || definition.Outputs[1].Name != "extra" {
		t.Errorf("outputs = %+v, want the two the manifest declared", definition.Outputs)
	}
	if len(definition.Credentials) != 1 || definition.Credentials[0].Type != "httpHeaderAuth" || !definition.Credentials[0].Required {
		t.Errorf("credentials = %+v, want the manifest's requirement", definition.Credentials)
	}
	if len(definition.Inputs) != 1 || definition.Inputs[0].Name != "main" {
		t.Errorf("inputs = %+v, want one main input", definition.Inputs)
	}
}

// A module replaces the resource cascade rather than adding to it, and it is
// not a trigger either.
func TestAModulePackMayNotAlsoBeARoutingOrTriggerPack(t *testing.T) {
	withResources := modulePack(t, []byte("bytes"), func(pack *Pack) {
		pack.Resources = []Resource{{Name: "thing", Operations: []Operation{{Name: "get"}}}}
	})
	if _, _, err := Load(withResources); err == nil || !strings.Contains(err.Error(), "both a module pack and a resource pack") {
		t.Errorf("Load(resources+module) error = %v, want the combination refused", err)
	}
	withTrigger := modulePack(t, []byte("bytes"), func(pack *Pack) {
		pack.Trigger = &Trigger{Events: []string{"message"}}
	})
	if _, _, err := Load(withTrigger); err == nil || !strings.Contains(err.Error(), "both a module pack and a trigger pack") {
		t.Errorf("Load(trigger+module) error = %v, want the combination refused", err)
	}
}

// A capability the host does not implement is refused by name, because the
// alternative is a module that imports nothing and silently does nothing.
func TestAModulePackRefusesAnUnknownCapabilityAndNamesIt(t *testing.T) {
	pack := modulePack(t, []byte("bytes"), func(pack *Pack) {
		pack.Module.Capabilities = []string{"http", "shell"}
	})
	issues := checkModule(pack, defaultCredentialLookup)
	if len(issues) != 1 {
		t.Fatalf("issues = %+v, want exactly one", issues)
	}
	if !strings.Contains(issues[0].Message, `"shell"`) || !strings.Contains(issues[0].Message, "binary.read") {
		t.Errorf("issue = %q, want it to name the capability and the ones that exist", issues[0].Message)
	}
	if issues[0].Path != "$.module.capabilities[1]" {
		t.Errorf("path = %q, want the entry's own path", issues[0].Path)
	}
}

// A credential type that cannot sign an HTTP request is refused: a module's
// only way out of the sandbox is an authenticated request, so a database
// credential would give it a secret it can do nothing legitimate with.
func TestAModulePackRefusesADatabaseCredentialType(t *testing.T) {
	pack := modulePack(t, []byte("bytes"), func(pack *Pack) {
		pack.Module.Credentials = []ModuleCredential{{Type: "postgres"}}
	})
	issues := checkModule(pack, defaultCredentialLookup)
	if len(issues) != 1 {
		t.Fatalf("issues = %+v, want exactly one", issues)
	}
	if !strings.Contains(issues[0].Message, "does not sign an HTTP request") {
		t.Errorf("issue = %q, want the request-signing reason", issues[0].Message)
	}
}

// Limits above the ceiling are refused, and the refusal says which bound.
func TestAModulePackRefusesLimitsAboveTheCeiling(t *testing.T) {
	pack := modulePack(t, []byte("bytes"), func(pack *Pack) {
		pack.Module.Limits = &ModuleLimits{TimeoutSeconds: 3600}
	})
	issues := checkModule(pack, defaultCredentialLookup)
	if len(issues) != 1 || !strings.Contains(issues[0].Message, "ceiling") {
		t.Fatalf("issues = %+v, want the ceiling refusal", issues)
	}
	if _, _, err := Load(pack); err == nil || !strings.Contains(err.Error(), "ceiling") {
		t.Errorf("Load() error = %v, want the same refusal from the server path", err)
	}
}

// A module pack cannot claim the built-in namespace.
func TestAModulePackClaimingTheBuiltinNamespaceIsRefused(t *testing.T) {
	pack := modulePack(t, []byte("bytes"), func(pack *Pack) {
		pack.Type = node.BuiltinPrefix + "steal"
	})
	issues := checkStructure(pack, "pack.json")
	if len(issues) == 0 || !strings.Contains(issues[0].Message, "reserved") {
		t.Errorf("issues = %+v, want the reserved-namespace refusal", issues)
	}
}

// Register is the entry point a caller with a manifest and no directory uses,
// and it refuses a module pack: the bytes live beside the manifest.
func TestRegisterRefusesAModulePackOutsideALoader(t *testing.T) {
	pack := modulePack(t, []byte("bytes"), nil)
	err := Register(node.NewRegistry(), nil, nil, nil, pack)
	if err == nil || !strings.Contains(err.Error(), "module packs load only from a pack directory") {
		t.Errorf("Register(module pack) error = %v, want it refused", err)
	}
}

// Every problem is reported at once, with its path: an author fixing one
// unknown field per run is the failure the validator exists to prevent.
func TestValidateReportsEveryModuleProblemAtOnceWithPaths(t *testing.T) {
	pack := modulePack(t, []byte("bytes"), func(pack *Pack) {
		pack.Module.File = "../escape.wasm"
		pack.Module.SHA256 = "not-a-digest"
		pack.Module.ABI = "v0"
		pack.Module.Mode = "parallel"
		pack.Module.Outputs = []ModuleOutput{{Name: "Main"}}
		pack.Module.Capabilities = []string{"ftp"}
		pack.Module.Credentials = []ModuleCredential{{Type: "nope"}}
	})
	issues := checkModule(pack, defaultCredentialLookup)
	paths := map[string]bool{}
	for _, issue := range issues {
		paths[issue.Path] = true
	}
	for _, want := range []string{
		"$.module.file", "$.module.sha256", "$.module.abi", "$.module.mode",
		"$.module.outputs[0].name", "$.module.capabilities[0]", "$.module.credentials[0].type",
	} {
		if !paths[want] {
			t.Errorf("no issue at %s: %+v", want, issues)
		}
	}
	if len(issues) != 7 {
		t.Errorf("%d issues reported, want 7 (one per problem)", len(issues))
	}
}
