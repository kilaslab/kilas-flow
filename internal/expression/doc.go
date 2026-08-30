// Package expression evaluates the kilasflow expression subset used in node
// parameters, for example {{ $json.name }} and {{ $node["Get User"].json.id }}.
//
// The evaluator is deliberately not a JavaScript engine: parameters come from
// workflow documents that may be authored by tenants of a host SaaS, so the
// grammar is restricted to data access rather than arbitrary code.
//
// Milestone 2.
package expression
