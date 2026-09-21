package nodepack

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/kilaslab/kilas-flow/internal/credentials"
	"github.com/kilaslab/kilas-flow/internal/wasmpack"
	sdk "github.com/kilaslab/kilas-flow/pkg/sdk"
)

// Module is the manifest of a WebAssembly node pack: a wasip1 module plus what
// it declares it needs.
//
// A module pack is the third kind of pack beside a declarative action pack and
// a trigger pack, and it is the only one that runs code the repository did not
// write. Everything the module may do is named here — its capabilities, the
// credential types it may use, and the bounds on its wall clock, memory,
// output and host calls — because the operator approves this file by reading
// it, and the host enforces it by registering only what it grants.
//
// The declaration is checked at load time, in the server, with the same rules
// the `nodepack` tool applies: a manifest the tool accepts is a manifest the
// server accepts, and neither can drift from the other without a test failing.
type Module struct {
	// File is the module's filename inside the pack directory. A bare name,
	// because a pack is one directory and a path would let a manifest reach
	// outside it.
	File string `json:"file"`
	// SHA256 is the digest of that file, so an installed pack is pinned to the
	// bytes the operator read. A mismatch refuses the load.
	SHA256 string `json:"sha256"`
	// ABI is the host ABI the module was built against. It must equal the ABI
	// this build speaks; a module built against another one is refused rather
	// than run against an interface it was not compiled for.
	ABI string `json:"abi"`
	// Mode is how the module is called: "item" (the default) runs it once per
	// input item, "batch" runs it once with every item.
	Mode string `json:"mode,omitempty"`
	// Outputs names the module's output ports. The first is the main one.
	Outputs []ModuleOutput `json:"outputs,omitempty"`
	// Capabilities are the host functions the module may call. Empty is a
	// module that can reshape items and nothing else.
	Capabilities []string `json:"capabilities,omitempty"`
	// Credentials are the credential types the module may name and read
	// non-secret fields from.
	Credentials []ModuleCredential `json:"credentials,omitempty"`
	// Limits bound one run. A field left zero takes the shipped default.
	Limits *ModuleLimits `json:"limits,omitempty"`
}

// ModuleOutput is one output port a module declares.
type ModuleOutput struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName,omitempty"`
}

// ModuleCredential is one credential type a module may use.
type ModuleCredential struct {
	Type string `json:"type"`
	// Required means the node cannot run without it, which is what the
	// compiler's required-credentials check reads.
	Required bool `json:"required,omitempty"`
}

// ModuleLimits is a module's own bounds, in the units a manifest reads in.
//
// Seconds and pages rather than Go durations and bytes because this is a file
// a person writes and reviews; the conversion to wasmpack.Limits happens once,
// where the ceilings are applied.
type ModuleLimits struct {
	TimeoutSeconds int    `json:"timeoutSeconds,omitempty"`
	MemoryPages    uint32 `json:"memoryPages,omitempty"`
	MaxOutputBytes int64  `json:"maxOutputBytes,omitempty"`
	MaxHostCalls   int    `json:"maxHostCalls,omitempty"`
}

// digestOf is the SHA-256 a manifest pins a module with. It lives here rather
// than in the pack runtime because both the loader and the tool check the same
// bytes against the same field.
func digestOf(module []byte) string {
	sum := sha256.Sum256(module)
	return hex.EncodeToString(sum[:])
}

// ModuleModes are the calling conventions a module may declare.
var ModuleModes = []string{"item", "batch"}

// ModuleCapabilities are the capability names a manifest may declare.
var ModuleCapabilities = []string{"http", "binary.read", "binary.write"}

// moduleFilePattern is a bare filename ending in .wasm: no directory
// separators, no parent references.
var moduleFilePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*\.wasm$`)

// moduleOutputPattern is the port-name grammar the editor and the compiler
// share.
var moduleOutputPattern = regexp.MustCompile(`^[a-z][A-Za-z0-9_]*$`)

// ModuleOutputLimit bounds how many output ports one module may declare.
const ModuleOutputLimit = 16

// digestPattern is a lowercase hex SHA-256.
var digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// credentialLookup resolves a credential type the deployment knows.
//
// It is a parameter rather than a call to credentials.Default() so the tool
// can validate against the registry it built and the server against the one it
// runs with.
type credentialLookup func(id string) (credentials.Type, bool)

// defaultCredentialLookup is the process-wide registry.
func defaultCredentialLookup(id string) (credentials.Type, bool) {
	return credentials.Default().Get(id)
}

// limits converts the manifest's bounds into the host's, filling what the
// manifest left zero from the shipped defaults and refusing anything above the
// ceilings.
func (module *Module) limits() (wasmpack.Limits, error) {
	limits := wasmpack.DefaultLimits()
	if module.Limits != nil {
		if module.Limits.TimeoutSeconds > 0 {
			limits.Timeout = time.Duration(module.Limits.TimeoutSeconds) * time.Second
		}
		if module.Limits.MemoryPages > 0 {
			limits.MemoryPages = module.Limits.MemoryPages
		}
		if module.Limits.MaxOutputBytes > 0 {
			limits.MaxOutputBytes = module.Limits.MaxOutputBytes
		}
		if module.Limits.MaxHostCalls > 0 {
			limits.MaxHostCalls = module.Limits.MaxHostCalls
		}
	}
	if err := limits.Validate(); err != nil {
		return wasmpack.Limits{}, err
	}
	return limits, nil
}

// mode is the calling convention, defaulted.
func (module *Module) mode() string {
	if strings.TrimSpace(module.Mode) == "" {
		return "item"
	}
	return strings.TrimSpace(module.Mode)
}

// outputs are the declared ports, defaulted to one main port.
func (module *Module) outputs() []ModuleOutput {
	if len(module.Outputs) == 0 {
		return []ModuleOutput{{Name: "main", DisplayName: "Output"}}
	}
	return module.Outputs
}

// capabilities parses the declared capability names.
func (module *Module) capabilities() (wasmpack.Capabilities, error) {
	caps := wasmpack.Capabilities{}
	for _, name := range module.Capabilities {
		switch strings.TrimSpace(name) {
		case "http":
			caps.HTTP = true
		case "binary.read":
			caps.BinaryRead = true
		case "binary.write":
			caps.BinaryWrite = true
		default:
			return wasmpack.Capabilities{}, fmt.Errorf("unknown capability %q: want one of %s",
				name, strings.Join(ModuleCapabilities, ", "))
		}
	}
	for _, credential := range module.credentials() {
		caps.Credentials = append(caps.Credentials, credential.Type)
	}
	return caps, nil
}

// credentials merges the module's own list with the pack's top-level
// credentialType, which is how a pack written before this field existed names
// the one credential it uses.
func (module *Module) credentials() []ModuleCredential {
	merged := make([]ModuleCredential, 0, len(module.Credentials)+1)
	seen := map[string]bool{}
	for _, credential := range module.Credentials {
		entry := ModuleCredential{Type: strings.TrimSpace(credential.Type), Required: credential.Required}
		if entry.Type == "" || seen[entry.Type] {
			continue
		}
		seen[entry.Type] = true
		merged = append(merged, entry)
	}
	return merged
}

// checkModule reports every problem a module manifest has, with the path each
// sits at.
//
// It is one function so Validate (the tool) and Load (the server) cannot
// disagree about what a valid module pack is: a manifest that validates here
// loads, and one that does not is refused with the same sentence in both
// places.
func checkModule(pack *Pack, lookup credentialLookup) []Issue {
	var issues []Issue
	fail := func(path, format string, args ...any) {
		issues = append(issues, Issue{Path: path, Message: fmt.Sprintf(format, args...)})
	}
	module := pack.Module
	if module == nil {
		return issues
	}
	if len(pack.Resources) > 0 {
		fail("$.module", "node pack %q is both a module pack and a resource pack: a module replaces the resource/operation cascade rather than adding to it", pack.Type)
	}
	if pack.Trigger != nil {
		fail("$.module", "node pack %q is both a module pack and a trigger pack", pack.Type)
	}

	file := strings.TrimSpace(module.File)
	switch {
	case file == "":
		fail("$.module.file", "a module pack names the module file it ships")
	case !moduleFilePattern.MatchString(file):
		fail("$.module.file", "%q is not a bare .wasm filename: the module sits beside the manifest, and a path would let it reach outside the pack directory", file)
	}

	switch digest := strings.TrimSpace(module.SHA256); {
	case digest == "":
		fail("$.module.sha256", "a module pack pins its module by digest")
	case !digestPattern.MatchString(digest):
		fail("$.module.sha256", "%q is not a lowercase hex SHA-256 digest", digest)
	}

	if abi := strings.TrimSpace(module.ABI); abi != sdk.ABIVersion {
		fail("$.module.abi", "this pack declares ABI %q but this build speaks %q: rebuild it against the current sdk", abi, sdk.ABIVersion)
	}

	if mode := module.mode(); !slices.Contains(ModuleModes, mode) {
		fail("$.module.mode", "unknown mode %q: want one of %s", module.Mode, strings.Join(ModuleModes, ", "))
	}

	outputs := module.outputs()
	if len(outputs) > ModuleOutputLimit {
		fail("$.module.outputs", "%d output ports declared, the limit is %d", len(outputs), ModuleOutputLimit)
	}
	seenOutput := map[string]bool{}
	for index, output := range outputs {
		name := strings.TrimSpace(output.Name)
		switch {
		case name == "":
			fail(fmt.Sprintf("$.module.outputs[%d].name", index), "an output port needs a name")
		case !moduleOutputPattern.MatchString(name):
			fail(fmt.Sprintf("$.module.outputs[%d].name", index), "%q is not a port name: lowerCamelCase, starting with a lowercase letter", name)
		case seenOutput[name]:
			fail(fmt.Sprintf("$.module.outputs[%d].name", index), "output port %q is declared twice", name)
		default:
			seenOutput[name] = true
		}
	}

	seenCapability := map[string]bool{}
	for index, capability := range module.Capabilities {
		name := strings.TrimSpace(capability)
		if seenCapability[name] {
			fail(fmt.Sprintf("$.module.capabilities[%d]", index), "capability %q is declared twice", name)
			continue
		}
		seenCapability[name] = true
		if !slices.Contains(ModuleCapabilities, name) {
			fail(fmt.Sprintf("$.module.capabilities[%d]", index), "unknown capability %q: want one of %s",
				name, strings.Join(ModuleCapabilities, ", "))
		}
	}

	seenCredential := map[string]bool{}
	for index, credential := range module.Credentials {
		id := strings.TrimSpace(credential.Type)
		if id == "" {
			fail(fmt.Sprintf("$.module.credentials[%d].type", index), "a credential entry names its type")
			continue
		}
		if seenCredential[id] {
			fail(fmt.Sprintf("$.module.credentials[%d].type", index), "credential type %q is declared twice", id)
			continue
		}
		seenCredential[id] = true
		checkModuleCredential(fail, fmt.Sprintf("$.module.credentials[%d].type", index), id, lookup)
	}
	if pack.CredentialType != "" {
		id := strings.TrimSpace(pack.CredentialType)
		if seenCredential[id] {
			fail("$.credentialType", "credential type %q is named both at the top level and in the module's own list", id)
		} else {
			checkModuleCredential(fail, "$.credentialType", id, lookup)
		}
	}

	if module.Limits != nil {
		limits := wasmpack.Limits{
			Timeout:        time.Duration(module.Limits.TimeoutSeconds) * time.Second,
			MemoryPages:    module.Limits.MemoryPages,
			MaxOutputBytes: module.Limits.MaxOutputBytes,
			MaxHostCalls:   module.Limits.MaxHostCalls,
		}
		if _, err := module.limits(); err != nil {
			fail("$.module.limits", "%v", err)
		} else if limits.Timeout < 0 || limits.MemoryPages < 0 || limits.MaxOutputBytes < 0 || limits.MaxHostCalls < 0 {
			fail("$.module.limits", "limits cannot be negative")
		}
	}

	// A module's own parameters are the ones the operator sees on the node, so
	// the resource/operation scoping a declarative pack uses would silently
	// hide them: a module has no cascade to scope by.
	for index, parameter := range pack.Parameters {
		if len(parameter.Resources) > 0 || len(parameter.Operations) > 0 {
			fail(fmt.Sprintf("$.parameters[%d]", index), "parameter %q scopes itself to resources or operations, which a module pack has none of: show or hide it in the module instead", parameter.Key)
		}
	}
	if len(pack.RequestDefaults.Method) > 0 || len(pack.RequestDefaults.URL) > 0 {
		fail("$.requestDefaults", "a module pack makes its own requests: requestDefaults is for declarative packs")
	}
	return issues
}

// checkModuleCredential reports whether one credential type is usable by a
// module.
func checkModuleCredential(fail func(path, format string, args ...any), path, id string, lookup credentialLookup) {
	credentialType, known := lookup(id)
	if !known {
		fail(path, "credential type %q is not registered in this build", id)
		return
	}
	if credentialType.Authenticate == nil {
		fail(path, "credential type %q does not sign an HTTP request, so a module cannot use it: a module's only way out is an authenticated request", id)
	}
}

// modulePath is the path the tool reports a module file at.
func modulePath(dir, file string) string {
	return path.Join(dir, file)
}
