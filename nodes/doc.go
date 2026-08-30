// Package nodes is the root of kilasflow's built-in node implementations.
//
// Each node lives in its own subpackage and registers itself with the registry
// in internal/node. The layout mirrors the categories the editor shows:
//
//	nodes/core/      manual, set, ifnode, merge, code
//	nodes/http/      HTTP Request
//	nodes/webhook/   Webhook Trigger, Respond to Webhook
//	nodes/database/  postgres, mysql, sqlite
//	nodes/ai/        chatmodel, agent, memory, tool
//
// Node types are namespaced as "kilasflow.<name>", for example kilasflow.http. The n8n
// importer maps foreign type names onto these.
//
// Milestone 1 onward.
package nodes
