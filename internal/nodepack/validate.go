// Validate is the author-facing half of the loader contract.
//
// Decode fails on the first unknown field with a sentence naming only the
// field, and Load and Register each return the first rule a pack breaks. One
// round trip per mistake is a miserable way to author a file, so this runs
// every check it can and reports every problem at once, each with the file
// and JSON path it came from. The rules themselves are not restated here:
// structural conversion runs the real Load, registration runs the real
// Register and RegisterTrigger against throwaway registries, and the
// reserved-namespace refusal reuses node.BuiltinPrefix, so the tool and the
// server can never disagree about what a pack may say.
package nodepack

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/property"
	"github.com/kilaslab/kilas-flow/internal/routing"
	"github.com/kilaslab/kilas-flow/internal/webhook"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// Issue is one problem in a pack file: where it is and what was expected.
// File and Path together are the address — a manifest path plus a JSON path
// like `$.resources[0].operations[1].method` — so a report never leaves an
// author guessing which field of which file broke.
type Issue struct {
	File    string
	Path    string
	Message string
}

func (issue Issue) Error() string {
	return issue.File + ": " + issue.Path + ": " + issue.Message
}

// Validate checks a decoded pack against every rule the server would enforce
// at registration, collecting all failures instead of stopping at the first.
// filename names the file the pack came from and appears in every issue.
func Validate(pack *Pack, filename string) []Issue {
	if filename == "" {
		filename = ManifestName
	}
	if pack == nil {
		return []Issue{{File: filename, Path: "$", Message: "a node pack is required"}}
	}
	issues := checkStructure(pack, filename)
	definition, description, err := Load(pack)
	_ = description
	if err != nil {
		return append(issues, Issue{File: filename, Path: "$", Message: err.Error()})
	}
	_ = definition
	if err := registerThrowaway(pack); err != nil {
		issues = append(issues, Issue{File: filename, Path: "$", Message: err.Error()})
	}
	return issues
}

// ValidateBytes checks raw manifest bytes: unknown fields with their paths,
// then everything Validate checks. A file that is not JSON, or whose types
// do not fit the format at all, yields one issue rather than a cascade of
// guesses derived from a pack that never decoded.
func ValidateBytes(data []byte, filename string) []Issue {
	if filename == "" {
		filename = ManifestName
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return []Issue{{File: filename, Path: "$", Message: fmt.Sprintf("not valid JSON: %v", err)}}
	}
	var pack Pack
	if err := json.Unmarshal(data, &pack); err != nil {
		return []Issue{{File: filename, Path: "$", Message: err.Error()}}
	}
	issues := checkUnknownFields(raw, filename)
	issues = append(issues, Validate(&pack, filename)...)
	return issues
}

// ValidateFile checks one pack.json file, or the pack.json inside a pack
// directory.
func ValidateFile(path string) []Issue {
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		path = filepath.Join(path, ManifestName)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return []Issue{{File: path, Path: "$", Message: fmt.Sprintf("cannot read pack: %v", err)}}
	}
	return ValidateBytes(data, path)
}

// ValidateDir checks one installed pack directory the way LoadDir would read
// it — checksum sidecar first, then the manifest — naming the pack directory
// file each problem came from.
func ValidateDir(dir, name string) []Issue {
	manifestPath := filepath.Join(dir, name, ManifestName)
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		if os.IsNotExist(err) {
			return []Issue{{File: manifestPath, Path: "$", Message: fmt.Sprintf("pack %q: %s is missing", name, ManifestName)}}
		}
		return []Issue{{File: manifestPath, Path: "$", Message: fmt.Sprintf("pack %q: %v", name, err)}}
	}
	if issues := checkDigest(dir, name, manifest); len(issues) > 0 {
		return issues
	}
	return ValidateBytes(manifest, manifestPath)
}

// checkDigest mirrors the loader's checksum refusal, naming the sidecar
// rather than aborting the boot.
func checkDigest(dir, name string, manifest []byte) []Issue {
	checksumPath := filepath.Join(dir, name, ChecksumName)
	recorded, err := os.ReadFile(checksumPath)
	if err != nil {
		if os.IsNotExist(err) {
			return []Issue{{File: checksumPath, Path: "$",
				Message: fmt.Sprintf("pack %q: %s is missing: generate it with `sha256sum pack.json`", name, ChecksumName)}}
		}
		return []Issue{{File: checksumPath, Path: "$", Message: fmt.Sprintf("pack %q: %v", name, err)}}
	}
	fields := strings.Fields(string(recorded))
	if len(fields) == 0 {
		return []Issue{{File: checksumPath, Path: "$",
			Message: fmt.Sprintf("pack %q: %s holds no digest", name, ChecksumName)}}
	}
	want, err := hex.DecodeString(fields[0])
	if err != nil || len(want) != sha256.Size {
		return []Issue{{File: checksumPath, Path: "$",
			Message: fmt.Sprintf("pack %q: %s is not a SHA-256 hex digest", name, ChecksumName)}}
	}
	if sum := sha256.Sum256(manifest); string(sum[:]) != string(want) {
		return []Issue{{File: checksumPath, Path: "$",
			Message: fmt.Sprintf("pack %q: %s no longer matches its recorded digest: regenerate it or restore the approved manifest",
				name, ManifestName)}}
	}
	return nil
}

// registerThrowaway runs the real registration path against registries that
// are discarded, so what the validator accepts is what the server accepts:
// known property kinds, valid visibility, subtitle templates, XSS-safe icons,
// known connection kinds, duplicate keys, and the routing subset's refusal of
// JavaScript hooks. The routing interpreter is stubbed in — a pack is always
// bound to it, so its absence would be a harness gap rather than a finding.
func registerThrowaway(pack *Pack) error {
	definitions := node.NewRegistry()
	routes := routing.NewRegistry()
	executors := engine.NewRegistry()
	stub := engine.ExecutorFunc(
		func(context.Context, workflow.IRNode, workflow.NodeInput, engine.Request) (workflow.NodeOutput, error) {
			return nil, nil
		})
	if err := executors.Register(routing.ExecutorID, stub); err != nil {
		return err
	}
	// Trigger packs name the fan-out executor rather than the routing
	// interpreter; without its stub every trigger pack fails validation
	// while the server would accept it.
	if err := executors.Register(TriggerExecutorID, stub); err != nil {
		return err
	}
	if err := Register(definitions, routes, executors, nil, pack); err != nil {
		return err
	}
	if pack.Trigger != nil {
		if err := RegisterTrigger(NewTriggerRegistry(), webhook.NewRegistry(), webhook.NewLifecycleRegistry(), pack); err != nil {
			return err
		}
	}
	return nil
}

// checkStructure names the problems Load would return one at a time —
// missing fields, duplicated keys, reserved names — each with its path, so
// one run lists them all.
func checkStructure(pack *Pack, filename string) []Issue {
	var issues []Issue
	fail := func(path, format string, args ...any) {
		issues = append(issues, Issue{File: filename, Path: path, Message: fmt.Sprintf(format, args...)})
	}
	if strings.TrimSpace(pack.Type) == "" {
		fail("$.type", "a node pack needs a type")
	} else if strings.HasPrefix(pack.Type, node.BuiltinPrefix) {
		fail("$.type", "node type %q claims the reserved %q namespace, which only built-in nodes may use",
			pack.Type, node.BuiltinPrefix)
	}
	if strings.TrimSpace(pack.DisplayName) == "" {
		fail("$.displayName", "a node pack needs a display name")
	}
	if strings.TrimSpace(pack.Category) == "" {
		fail("$.category", "a node pack needs a category")
	}
	if pack.Version.IsZero() {
		fail("$.version", "a node pack needs a positive version")
	}
	if pack.Trigger != nil && len(pack.Resources) > 0 {
		fail("$.trigger", "node pack %q is both a trigger and an action node", pack.Type)
	}
	if pack.Trigger == nil && len(pack.Resources) == 0 {
		fail("$.resources", "node pack %q declares neither resources nor a trigger", pack.Type)
	}
	seenResource := map[string]bool{}
	for i, resource := range pack.Resources {
		base := fmt.Sprintf("$.resources[%d]", i)
		if strings.TrimSpace(resource.Name) == "" {
			fail(base+".name", "a resource needs a name")
		} else if seenResource[resource.Name] {
			fail(base+".name", "node pack %q declares resource %q twice", pack.Type, resource.Name)
		} else {
			seenResource[resource.Name] = true
		}
		if len(resource.Operations) == 0 {
			fail(base+".operations", "node pack %q resource %q declares no operations", pack.Type, resource.Name)
		}
		seenOperation := map[string]bool{}
		for j, operation := range resource.Operations {
			at := fmt.Sprintf("%s.operations[%d]", base, j)
			if strings.TrimSpace(operation.Name) == "" {
				fail(at+".name", "an operation needs a name")
			} else if seenOperation[operation.Name] {
				fail(at+".name", "node pack %q resource %q declares operation %q twice",
					pack.Type, resource.Name, operation.Name)
			} else {
				seenOperation[operation.Name] = true
			}
			if strings.TrimSpace(operation.Method) == "" {
				fail(at+".method", "operation %q needs an HTTP method", operation.Name)
			}
			if strings.TrimSpace(operation.URL) == "" {
				fail(at+".url", "operation %q needs a URL", operation.Name)
			}
		}
	}
	seenParameter := map[string]bool{}
	for i, parameter := range pack.Parameters {
		at := fmt.Sprintf("$.parameters[%d]", i)
		if strings.TrimSpace(parameter.Key) == "" {
			fail(at+".key", "a parameter needs a key")
		} else if parameter.Key == ResourceKey || parameter.Key == OperationKey {
			fail(at+".key", "%q is reserved for the pack's own cascade", parameter.Key)
		} else if seenParameter[parameter.Key] {
			fail(at+".key", "node pack %q declares parameter %q twice", pack.Type, parameter.Key)
		} else {
			seenParameter[parameter.Key] = true
		}
		if strings.TrimSpace(string(parameter.Kind)) == "" {
			fail(at+".kind", "parameter %q needs a kind", parameter.Key)
		} else if !property.Known(parameter.Kind) {
			fail(at+".kind", "parameter %q has unknown kind %q: want one of %s",
				parameter.Key, string(parameter.Kind), knownKindNames())
		}
	}
	return issues
}

var (
	packKeys       = []string{"type", "version", "displayName", "description", "category", "icon", "iconColor", "subtitle", "documentationUrl", "credentialType", "requestDefaults", "trigger", "parameters", "resources", "generator"}
	resourceKeys   = []string{"name", "description", "operations"}
	operationKeys  = []string{"name", "description", "method", "url", "sends", "output", "pagination"}
	parameterKeys  = []string{"key", "label", "description", "kind", "required", "default", "options", "typeOptions", "resources", "operations"}
	provenanceKeys = []string{"tool", "source", "sourceTitle", "sourceVersion", "sourceDigest"}
	triggerKeys    = []string{"events", "catchAll", "eventPath", "shape", "webhook", "hmac", "lifecycle", "media", "notice"}
)

// checkUnknownFields lists every field the strict decoder would refuse, with
// the path each sits at. The decoder aborts on the first; an author fixing
// one unknown field per run is the failure this exists to prevent. Blobs
// owned by other packages — request defaults, sends, outputs, options — are
// strict-decoded into their real types rather than relisted here, so a new
// routing field cannot drift out of sync with this list unnoticed.
func checkUnknownFields(raw map[string]json.RawMessage, filename string) []Issue {
	var issues []Issue
	fail := func(path, format string, args ...any) {
		issues = append(issues, Issue{File: filename, Path: path, Message: fmt.Sprintf(format, args...)})
	}
	for key := range raw {
		if !slicesContain(packKeys, key) {
			fail("$.", "unknown field %q: want one of %s", key, strings.Join(packKeys, ", "))
		}
	}
	if blob, ok := raw["requestDefaults"]; ok {
		var defaults routing.Request
		if err := strictDecode(blob, &defaults); err != nil {
			fail("$.requestDefaults", "%v", err)
		}
	}
	if blob, ok := raw["generator"]; ok {
		failUnknown(blob, provenanceKeys, "$.generator", filename, &issues)
	}
	if blob, ok := raw["trigger"]; ok {
		var trigger Trigger
		if err := strictDecode(blob, &trigger); err != nil {
			fail("$.trigger", "%v", err)
		} else {
			failUnknown(blob, triggerKeys, "$.trigger", filename, &issues)
		}
	}
	if blob, ok := raw["resources"]; ok {
		var resources []map[string]json.RawMessage
		if err := json.Unmarshal(blob, &resources); err == nil {
			for i, resource := range resources {
				base := fmt.Sprintf("$.resources[%d]", i)
				failUnknownMap(resource, resourceKeys, base, filename, &issues)
				var operations []map[string]json.RawMessage
				if ops, ok := resource["operations"]; ok && json.Unmarshal(ops, &operations) == nil {
					for j, operation := range operations {
						at := fmt.Sprintf("%s.operations[%d]", base, j)
						failUnknownMap(operation, operationKeys, at, filename, &issues)
						if sends, ok := operation["sends"]; ok {
							var list []json.RawMessage
							if json.Unmarshal(sends, &list) == nil {
								for k, send := range list {
									var parsed routing.Send
									if err := strictDecode(send, &parsed); err != nil {
										fail(fmt.Sprintf("%s.sends[%d]", at, k), "%v", err)
									}
								}
							}
						}
						if out, ok := operation["output"]; ok {
							var parsed routing.Output
							if err := strictDecode(out, &parsed); err != nil {
								fail(at+".output", "%v", err)
							}
						}
						if pagination, ok := operation["pagination"]; ok {
							var parsed routing.Pagination
							if err := strictDecode(pagination, &parsed); err != nil {
								fail(at+".pagination", "%v", err)
							}
						}
					}
				}
			}
		}
	}
	if blob, ok := raw["parameters"]; ok {
		var parameters []map[string]json.RawMessage
		if err := json.Unmarshal(blob, &parameters); err == nil {
			for i, parameter := range parameters {
				at := fmt.Sprintf("$.parameters[%d]", i)
				failUnknownMap(parameter, parameterKeys, at, filename, &issues)
				if options, ok := parameter["options"]; ok {
					var list []json.RawMessage
					if json.Unmarshal(options, &list) == nil {
						for k, option := range list {
							var parsed property.PropertyOption
							if err := strictDecode(option, &parsed); err != nil {
								fail(fmt.Sprintf("%s.options[%d]", at, k), "%v", err)
							}
						}
					}
				}
				if typeOptions, ok := parameter["typeOptions"]; ok {
					var parsed property.TypeOptions
					if err := strictDecode(typeOptions, &parsed); err != nil {
						fail(at+".typeOptions", "%v", err)
					}
				}
			}
		}
	}
	return issues
}

func failUnknown(blob json.RawMessage, known []string, path, filename string, issues *[]Issue) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(blob, &object); err != nil {
		return
	}
	failUnknownMap(object, known, path, filename, issues)
}

func failUnknownMap(object map[string]json.RawMessage, known []string, path, filename string, issues *[]Issue) {
	for key := range object {
		if !slicesContain(known, key) {
			*issues = append(*issues, Issue{File: filename, Path: path,
				Message: fmt.Sprintf("unknown field %q: want one of %s", key, strings.Join(known, ", "))})
		}
	}
}

func strictDecode(blob json.RawMessage, value any) error {
	decoder := json.NewDecoder(strings.NewReader(string(blob)))
	decoder.DisallowUnknownFields()
	return decoder.Decode(value)
}

func slicesContain(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

func knownKindNames() string {
	kinds := property.KnownKinds()
	names := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		names = append(names, string(kind))
	}
	return strings.Join(names, ", ")
}
