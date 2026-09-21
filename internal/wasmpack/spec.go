package wasmpack

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// ExecutorID is the executor a module pack runs under.
//
// It is the only executor that runs a WebAssembly pack, and the loader is the
// only thing that may name it: a manifest that could name its own executor
// could claim any binding the server has registered.
const ExecutorID = "core.packModule"

// Mode is how a module is called.
type Mode string

const (
	// ModeItem runs the module once per input item. It is the default because
	// it is what a node that shapes one item at a time means, and because a
	// module that fails one item should fail that item rather than the batch.
	ModeItem Mode = "item"
	// ModeBatch runs the module once with every item, which is what a module
	// that aggregates or paginates needs.
	ModeBatch Mode = "batch"
)

// Spec is one module pack, ready to run.
//
// It is what the loader hands the registry: the module's bytes and digest, and
// everything the manifest declared about how it may be used. The registry
// audits it before it is reachable, so a pack that imports a host function it
// did not declare is refused at load time rather than failing a workflow later.
type Spec struct {
	// Type and Version are the node type the pack registers.
	Type    string
	Version workflow.TypeVersion
	// Module is the compiled wasip1 module.
	Module []byte
	// Digest is the SHA-256 of Module, as the manifest pinned it.
	Digest string
	// Mode is the calling convention.
	Mode Mode
	// Caps are the capabilities the manifest declared.
	Caps Capabilities
	// Limits bound one run.
	Limits Limits
	// Outputs are the ports the module may write to, the main one first.
	Outputs []string
}

// digestOf is the digest a manifest pins a module with.
func digestOf(module []byte) string {
	sum := sha256.Sum256(module)
	return hex.EncodeToString(sum[:])
}

// validate reports whether this spec is usable, before anything compiles it.
func (spec Spec) validate() error {
	if strings.TrimSpace(spec.Type) == "" {
		return fmt.Errorf("a module pack needs a node type")
	}
	if len(spec.Module) == 0 {
		return fmt.Errorf("module pack %q ships no module bytes", spec.Type)
	}
	if spec.Digest != "" && !strings.EqualFold(spec.Digest, digestOf(spec.Module)) {
		return fmt.Errorf("module pack %q does not match the digest its manifest pins: the file was changed after it was approved", spec.Type)
	}
	switch spec.Mode {
	case ModeItem, ModeBatch:
	case "":
		return fmt.Errorf("module pack %q declares no calling convention", spec.Type)
	default:
		return fmt.Errorf("module pack %q declares unknown mode %q", spec.Type, spec.Mode)
	}
	if err := spec.Limits.Validate(); err != nil {
		return fmt.Errorf("module pack %q: %w", spec.Type, err)
	}
	if err := spec.Caps.validate(); err != nil {
		return fmt.Errorf("module pack %q: %w", spec.Type, err)
	}
	if len(spec.Outputs) == 0 {
		return fmt.Errorf("module pack %q declares no output ports", spec.Type)
	}
	return nil
}
