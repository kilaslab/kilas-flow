// Package engine executes a compiled workflow graph.
//
// Dependency rules (PRD section 62). The engine must not import:
//   - internal/api        transport is a caller, not a dependency
//   - gorm.io/gorm        persistence is reached through repository interfaces
//   - agent-framework-go  the AI framework is reached through internal/ai
//
// Keeping these out is what lets persistence and the agent runtime be replaced
// without touching execution semantics.
//
// Milestone 1.
package engine
