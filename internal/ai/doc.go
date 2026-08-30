// Package ai defines kilasflow's agent contracts: AgentRuntime, requests, events,
// and memory.
//
// These types are the public boundary. No package outside internal/ai/maf may
// import an agent framework directly, so the framework stays an implementation
// detail rather than part of the workflow contract.
//
// Milestone 4.
package ai
