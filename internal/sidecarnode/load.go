package sidecarnode

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	"github.com/kilaslab/kilas-flow/internal/credentials"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/sidecar"
)

// NodeRef is what a loaded community node is, from the executor's point of
// view: which package it came from, what the package calls it, and what the
// run has to be told so the package dispatches to the right class.
//
// It deliberately does not carry the property list. The executor reads the
// properties through the node catalogue, which is the one copy the editor also
// reads: a second copy here would be a second answer to "is this field shown".
type NodeRef struct {
	// Package is the npm package name, for diagnostics.
	Package string
	// PackageVersion is the installed version, for diagnostics.
	PackageVersion string
	// Type and Version are the catalogue coordinates this node is registered
	// under.
	Type    string
	Version workflow.TypeVersion
	// Name is the package's own name for the node, and NodeVersion the version
	// it declared. Together they are the runner's dispatch key.
	Name        string
	NodeVersion float64
	// HiddenDefaults are the defaults of properties the editor never shows. A
	// package can still read one with getNodeParameter, so the value has to
	// reach the run even though it is not a stored parameter.
	HiddenDefaults map[string]any
	// CredentialTypes are the credential types this node declares. Only these
	// are resolved for a run: the compiler does not restrict the credential
	// keys a document may carry, and resolving a type the node never asked for
	// would hand third-party code a secret it has no business holding.
	CredentialTypes []string
}

type refKey struct {
	nodeType string
	version  workflow.TypeVersion
}

// Index is what one load produced: the dispatch table the executor looks nodes
// up in, plus every node and file that load left out.
type Index struct {
	refs       map[refKey]NodeRef
	exclusions []Exclusion
}

// NewIndex creates an empty index. Load builds one as it registers; a caller
// that converts packages itself fills it from Convert's output.
func NewIndex() *Index {
	return &Index{refs: map[refKey]NodeRef{}}
}

// Add indexes converted nodes. A later entry for the same (type, version)
// replaces the earlier one, which only happens when a caller converts the same
// package twice.
func (index *Index) Add(converted []Converted) {
	for _, one := range converted {
		index.refs[refKey{nodeType: one.Definition.Type, version: one.Definition.Version}] = one.Ref
	}
}

// Get returns the node a run targets. The lookup is exact: the engine has
// already resolved the document's version against the catalogue, so a
// near-miss here would mean the catalogue and the index disagree.
func (index *Index) Get(nodeType string, version workflow.TypeVersion) (NodeRef, bool) {
	if index == nil {
		return NodeRef{}, false
	}
	ref, found := index.refs[refKey{nodeType: nodeType, version: version}]
	return ref, found
}

// Len reports how many nodes the load registered.
func (index *Index) Len() int {
	if index == nil {
		return 0
	}
	return len(index.refs)
}

// Exclusions returns the nodes and files the load left out, in the order they
// were found. They are informational: an operator reads them to learn which
// node of a package this build cannot run, and why.
func (index *Index) Exclusions() []Exclusion {
	if index == nil {
		return nil
	}
	return append([]Exclusion(nil), index.exclusions...)
}

func (index *Index) exclude(exclusions []Exclusion) {
	index.exclusions = append(index.exclusions, exclusions...)
}

// LoadDeps is everything a load needs from the deployment.
type LoadDeps struct {
	// Spawn starts the sidecar process the catalogue is read from. A nil spawn
	// is a deployment that declined the sidecar: the load fails with the
	// package's own install diagnostic.
	Spawn sidecar.SpawnFunc
	// Limits bound the describe process.
	Limits sidecar.Limits
	// Definitions is the node catalogue the converted definitions are
	// registered into.
	Definitions *node.Registry
	// Credentials is the credential catalogue the converted credential types
	// are registered into.
	Credentials *credentials.Registry
	// SharedSettings are the settings every node carries (Continue on Fail,
	// Retry, Timeout, Always output data). They are supplied by the caller so
	// this package does not have to import the package that owns them.
	SharedSettings []node.PropertyDefinition
	// Log receives one Warn per exclusion. Nil discards them.
	Log *slog.Logger
}

// Load reads the installed packages through the sidecar, converts them, and
// registers what this build can run.
//
// Failure policy: a per-file load failure excludes that node and the load
// continues; a package-level failure refuses the boot, because nothing in that
// package can be trusted; a collision in the real catalogue — one node type
// from two packages, or a credential type already registered — is a naming
// problem only a human can settle, so it fails naming both packages.
func Load(ctx context.Context, deps LoadDeps) (*Index, error) {
	if deps.Definitions == nil {
		return nil, fmt.Errorf("load community node packages: a node catalogue is required")
	}
	if deps.Credentials == nil {
		return nil, fmt.Errorf("load community node packages: a credential catalogue is required")
	}
	log := deps.Log
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	catalogue, err := discover(ctx, deps.Spawn, deps.Limits)
	if err != nil {
		return nil, fmt.Errorf("load community node packages: %w", err)
	}

	index := NewIndex()
	nodeOwners := map[string]string{}
	credentialOwners := map[string]string{}

	for _, pkg := range catalogue.Packages {
		for _, issue := range pkg.Errors {
			if issue.Severity == severityFatal {
				return nil, fmt.Errorf("community package %q cannot be loaded: %s: %s", pkg.Name, issue.Code, issue.Message)
			}
		}

		converted, credentialTypes, exclusions, err := Convert(pkg)
		if err != nil {
			return nil, fmt.Errorf("community package %q: %w", pkg.Name, err)
		}
		// The runner's per-file failures are exclusions too: the file's node is
		// gone, the package is not.
		for _, issue := range pkg.Errors {
			exclusions = append(exclusions, Exclusion{
				Package: pkg.Name, File: issue.File,
				Reason: fmt.Sprintf("%s: %s", issue.Code, issue.Message),
			})
		}
		index.exclude(exclusions)
		for _, exclusion := range exclusions {
			log.Warn("community node excluded", "package", exclusion.Package, "file", exclusion.File,
				"node", exclusion.Node, "reason", exclusion.Reason)
		}

		for _, credentialType := range credentialTypes {
			if owner, taken := credentialOwners[credentialType.ID]; taken {
				return nil, fmt.Errorf("credential type %q is declared by both package %q and package %q; only one of them can be registered",
					credentialType.ID, owner, pkg.Name)
			}
			if existing, found := deps.Credentials.Get(credentialType.ID); found {
				return nil, fmt.Errorf("credential type %q from package %q is already registered by this deployment as %q",
					credentialType.ID, pkg.Name, existing.DisplayName)
			}
			if err := deps.Credentials.Register(credentialType); err != nil {
				return nil, fmt.Errorf("community package %q: %w", pkg.Name, err)
			}
			credentialOwners[credentialType.ID] = pkg.Name
		}

		for _, one := range converted {
			definition := one.Definition
			definition.SharedSettings = deps.SharedSettings
			key := definition.Type + "@" + definition.Version.String()
			if owner, taken := nodeOwners[key]; taken {
				return nil, fmt.Errorf("node type %q version %s is declared by both package %q and package %q; only one of them can be registered",
					definition.Type, definition.Version, owner, pkg.Name)
			}
			if existing, found := deps.Definitions.Get(definition.Type, definition.Version); found {
				return nil, fmt.Errorf("node type %q version %s from package %q is already registered as a %s node",
					definition.Type, definition.Version, pkg.Name, existing.Source)
			}
			if err := deps.Definitions.RegisterFrom(node.SourceSidecar, definition); err != nil {
				return nil, fmt.Errorf("community package %q: %w", pkg.Name, err)
			}
			nodeOwners[key] = pkg.Name
			index.Add([]Converted{one})
		}
	}
	return index, nil
}
