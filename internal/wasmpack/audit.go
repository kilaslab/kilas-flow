package wasmpack

import (
	"context"
	"fmt"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/runcode"
	sdk "github.com/kilaslab/kilas-flow/pkg/sdk"
)

// Report is what an audit found, for the operator installing a pack.
//
// Declared and Imported are both listed because the interesting case is where
// they differ: a pack that declares a capability it never reaches is asking for
// more than it needs, and an operator who can see that can ask the author to
// narrow it.
type Report struct {
	// Declared is the manifest's capabilities, one credential type per entry.
	Declared []string
	// Imported is the host functions the module actually imports, in the order
	// it declares them.
	Imported []string
	// Exports is every name the module exports.
	Exports []string
}

// Audit decides whether a module may be installed with these capabilities.
//
// It compiles the module — which needs no imports to be resolved, so nothing is
// registered and nothing runs — and reads what the module asks for. Every
// import must come from WASI or from the host module, every host function must
// be in the ABI, and every one of those must be granted by the manifest.
//
// This is the load-time half of the capability boundary; the run-time half is
// that only granted functions are registered at all. Both exist on purpose: the
// audit is what turns a pack that reaches past its declaration into an
// installation error an operator sees, rather than a node run that fails at
// three in the morning.
func Audit(ctx context.Context, cache *runcode.ModuleCache, module []byte, caps Capabilities, limits Limits) (Report, error) {
	limits = limits.withDefaults()
	if err := limits.Validate(); err != nil {
		return Report{}, fmt.Errorf("pack limits: %w", err)
	}
	if err := caps.validate(); err != nil {
		return Report{}, err
	}

	inspection, err := cache.Inspect(ctx, module, limits.MemoryPages)
	if err != nil {
		// A module whose declared memory minimum is over the limit is refused
		// before it starts. wazero's own sentence renders the module's minimum
		// and the limit as the same rounded size, so the message states the
		// limit the manifest asked for.
		if strings.Contains(err.Error(), "over limit of") {
			return Report{}, fmt.Errorf("the module cannot start inside the %d-page memory limit this pack declares", limits.MemoryPages)
		}
		return Report{}, fmt.Errorf("the module could not be read: %w", err)
	}

	report := Report{Declared: caps.Names(), Exports: inspection.Exports}
	for _, required := range []string{"_start", "memory"} {
		if !containsString(inspection.Exports, required) {
			return report, fmt.Errorf("the module does not export %s, so it is not a WASI command module", required)
		}
	}
	for _, imported := range inspection.Imports {
		switch imported.Module {
		case "wasi_snapshot_preview1":
			// The guest's own runtime: standard streams and the clocks, which
			// the sandbox gives every module and which carry no capability.
			continue
		case sdk.HostModule:
			function, known := sdk.FunctionNamed(imported.Name)
			if !known {
				return report, fmt.Errorf("the module imports %s.%s, which is not part of ABI %s",
					sdk.HostModule, imported.Name, sdk.ABIVersion)
			}
			if !caps.Granted(function) {
				return report, fmt.Errorf("the module imports %s but the pack does not declare the %s capability",
					imported.Name, function.Capability)
			}
			report.Imported = append(report.Imported, imported.Name)
		default:
			return report, fmt.Errorf("the module imports from %q, which is not a module a pack may import", imported.Module)
		}
	}
	return report, nil
}

// validate reports whether a manifest's capabilities make sense.
//
// A declared credential type has to be one that can authenticate an HTTP
// request. That is what keeps a database credential — and with it the
// internal-database guard — out of a pack's reach by construction: the type is
// refused at installation, so no run ever gets the chance to name it.
func (caps Capabilities) validate() error {
	for _, credentialType := range caps.Credentials {
		if strings.TrimSpace(credentialType) == "" {
			return fmt.Errorf("the pack declares an empty credential type")
		}
		if !httpCapableCredential(credentialType) {
			return fmt.Errorf("the pack declares the %s credential, which cannot authenticate an HTTP request", credentialType)
		}
	}
	return nil
}

func containsString(haystack []string, needle string) bool {
	for _, entry := range haystack {
		if entry == needle {
			return true
		}
	}
	return false
}
