// Package credentials stores and resolves secrets referenced by workflows.
//
// Workflow documents carry only credential IDs. Payloads are encrypted at rest
// with AES-256-GCM using a master key supplied through the environment, so a
// workflow export never contains a plaintext secret.
//
// Milestone 2.
package credentials
