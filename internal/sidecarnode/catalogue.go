// Package sidecarnode turns the community packages a Node process loaded into
// node definitions this process can serve.
//
// It is the host side of the JavaScript sidecar: the runner (sidecar/) loads a
// package through its package.json `n8n` manifest and answers with a faithful
// record of what the package declares, and this package decides what of that
// record KilasFlow can actually run. Everything it cannot run is excluded with
// a named reason rather than half-mapped, because a node registered with a
// property shape the executor cannot marshal fails later, on somebody's
// workflow, instead of at boot.
//
// It deliberately does not import nodes/: the executor binding is an opaque
// string both packages agree on (ExecutorID), so the conversion has no opinion
// about how a definition is run.
package sidecarnode

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/kilaslab/kilas-flow/sidecar"
)

// ExecutorID is the engine binding every node this package produces names. A
// sidecar node is not one executor per package: the engine hands the node's
// compiled IR to one adapter, which dispatches into the tenant's sidecar
// process by (node name, version).
const ExecutorID = "sidecar.node"

// Catalogue is what one describe run answered with.
type Catalogue struct {
	NodeVersion string        `json:"nodeVersion"`
	Packages    []PackageInfo `json:"packages"`
}

// PackageInfo is one package the runner loaded, as the runner described it.
//
// The shape mirrors sidecar/runner/runner.cjs's catalogue exactly: a package's
// own file list and load errors, plus the nodes and credentials it declared.
// Nothing here is validated on the way in — a package is third-party data, so
// every field is treated as untrusted input by Convert.
type PackageInfo struct {
	Name        string              `json:"name"`
	Version     string              `json:"version"`
	Nodes       []PackageNode       `json:"nodes"`
	Credentials []PackageCredential `json:"credentials"`
	Errors      []PackageError      `json:"errors"`
}

// PackageNode is one node class the runner constructed.
type PackageNode struct {
	File string `json:"file"`
	// Name and Version are the runner's dispatch key: the description's own
	// `name` and the `version` it declared, which may be a number or an array
	// of numbers when one file carries several versions.
	Name    string          `json:"name"`
	Version json.RawMessage `json:"version"`
	// Execute reports whether the class has an execute() method. A class
	// without one has no run surface in v1.
	Execute bool `json:"execute"`
	// Unsupported names the methods the runner found that this sidecar does
	// not serve (trigger, poll, webhook, a nodeVersions wrapper).
	Unsupported []string `json:"unsupported"`
	// Description is the class's own description object. It is kept as raw
	// JSON rather than typed: a package is free to carry keys this build has
	// never heard of, and a typed struct with a closed field set would drop
	// them silently instead of refusing the shapes that matter.
	Description map[string]any `json:"description"`
}

// PackageCredential is one credential class the runner constructed.
type PackageCredential struct {
	File             string           `json:"file"`
	Name             string           `json:"name"`
	DisplayName      string           `json:"displayName"`
	DocumentationURL string           `json:"documentationUrl"`
	Properties       []map[string]any `json:"properties"`
}

// PackageError is one load problem the runner recorded. Severity is "file"
// when only the named file's node is lost, and "fatal" when the package as a
// whole could not be loaded.
type PackageError struct {
	Package  string `json:"package"`
	File     string `json:"file"`
	Code     string `json:"code"`
	Message  string `json:"message"`
	Severity string `json:"severity"`
}

const (
	// severityFile is a per-file failure: that node is excluded, the package
	// is not.
	severityFile = "file"
	// severityFatal is a package-level failure: nothing in it can be trusted,
	// so the boot is refused rather than quietly serving a partial package.
	severityFatal = "fatal"
)

// discover describes the community packages and decodes the catalogue.
//
// It is a named function rather than a line in Load so a test can exercise the
// decode without a Node process.
func discover(ctx context.Context, spawn sidecar.SpawnFunc, limits sidecar.Limits) (Catalogue, error) {
	raw, err := sidecar.Discover(ctx, spawn, limits)
	if err != nil {
		return Catalogue{}, err
	}
	var catalogue Catalogue
	if err := json.Unmarshal(raw, &catalogue); err != nil {
		return Catalogue{}, fmt.Errorf("the sidecar catalogue is not readable: %w", err)
	}
	return catalogue, nil
}
