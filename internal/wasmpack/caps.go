// Package wasmpack runs a community node pack's WebAssembly module.
//
// A pack is a wasip1 module plus a manifest. The manifest declares what the
// pack needs — capabilities, credential types, limits — and this package is
// what turns that declaration into the only things the module can actually do:
// the host functions registered for one run, the credential the host applies on
// the pack's behalf, the payloads it may read, and the bounds on its wall
// clock, memory, output and host calls.
//
// The shape of the boundary is deliberate and worth stating, because it is the
// whole security argument:
//
//   - A pack that declares no capability gets no host module. Its module cannot
//     import a host function, and if it does anyway the audit refuses it before
//     anything is registered — not at run time, where the failure would be a
//     workflow error rather than an installation error.
//   - Every host function is registered per run, into that run's runtime, from
//     the ABI table in pkg/sdk. There is no shared host module and no
//     long-lived capability: what one run could reach is not visible to the
//     next.
//   - Everything the guest says is untrusted. A pointer is bounds-checked
//     against the guest's own linear memory, a length is checked against a cap
//     before a byte is read, metadata is decoded strictly, and nothing is
//     written back except through result_read's bounds check.
//   - The host polices the call, not the guest: outbound HTTP goes through
//     internal/safehttp with the deployment's SSRF policy, the credential is
//     applied by the engine's own seam (the same one the HTTP node uses), and
//     the domain scope is checked before a byte leaves the process.
package wasmpack

import (
	"sort"

	sdk "github.com/kilaslab/kilas-flow/pkg/sdk"
)

// Capabilities is what a pack's manifest declares it needs.
//
// It is a declaration, not a permission: the operator approves it by reading
// and installing the manifest, and the host enforces it by registering only
// what it grants. An empty Capabilities is a pack that can reshape items and
// nothing else.
type Capabilities struct {
	// HTTP is outbound HTTP through the deployment's SSRF policy.
	HTTP bool
	// Credentials names the credential types the pack may read non-secret
	// fields from and name for the host to apply. Empty grants nothing: a pack
	// with an empty list cannot even name a credential.
	Credentials []string
	// BinaryRead is reading payloads the input items carry or this run wrote.
	BinaryRead bool
	// BinaryWrite is storing a payload and handing its reference back.
	BinaryWrite bool
}

// Granted reports whether these capabilities grant one ABI function.
//
// result_len and result_read are granted whenever anything else is, because
// every capability answers through the same two slots: a pack that may make a
// request but may not read the answer could not use the capability it was
// granted.
func (caps Capabilities) Granted(function sdk.Function) bool {
	switch function.Capability {
	case sdk.CapHTTP:
		return caps.HTTP
	case sdk.CapCredentials:
		return len(caps.Credentials) > 0
	case sdk.CapBinaryRead:
		return caps.BinaryRead
	case sdk.CapBinaryWrite:
		return caps.BinaryWrite
	case sdk.CapResult:
		return caps.any()
	default:
		// An ABI function whose capability this build does not know is granted
		// nothing. Refusing is the only safe answer for a capability that
		// arrived from a newer ABI table than this host's.
		return false
	}
}

// GrantedCredential reports whether one credential type was declared.
func (caps Capabilities) GrantedCredential(credentialType string) bool {
	for _, declared := range caps.Credentials {
		if declared == credentialType {
			return true
		}
	}
	return false
}

// any reports whether the pack declared any capability at all.
func (caps Capabilities) any() bool {
	return caps.HTTP || caps.BinaryRead || caps.BinaryWrite || len(caps.Credentials) > 0
}

// GrantedFunctions returns the ABI rows these capabilities grant, in table
// order, which is the order they are registered in.
func (caps Capabilities) GrantedFunctions() []sdk.Function {
	granted := make([]sdk.Function, 0, len(sdk.Functions))
	for _, function := range sdk.Functions {
		if caps.Granted(function) {
			granted = append(granted, function)
		}
	}
	return granted
}

// Names reports what the manifest declares, for an audit report an operator
// reads. The credential types are listed one by one, because "credentials"
// alone would not say which.
func (caps Capabilities) Names() []string {
	names := make([]string, 0, 4)
	for _, function := range sdk.Functions {
		capability := function.Capability
		if capability == sdk.CapResult || !caps.Granted(function) {
			continue
		}
		name := string(capability)
		if capability == sdk.CapCredentials {
			for _, credentialType := range caps.Credentials {
				names = append(names, name+":"+credentialType)
			}
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
