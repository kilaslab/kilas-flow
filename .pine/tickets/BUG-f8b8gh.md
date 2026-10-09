---
id: BUG-f8b8gh
title: 'The AI memory ceiling can evict the newest session: two sessions touched in one clock tick tie'
status: todo
priority: medium
parent: EPIC-8rbys7
created: "2026-10-09T13:33:28Z"
updated: "2026-10-09T13:33:28Z"
---

# Description

`BufferMemory`'s per-tenant session ceiling evicts the "least-recently-touched" session by comparing wall-clock `touched` times (internal/ai/memory.go `evictBeyondCeiling`). Two sessions touched within one clock tick tie, and Go's random map order then decides which one is dropped — sometimes the newest conversation instead of the oldest. `TestPerTenantCeilingEvictsTheOldestSessionFirst` failed intermittently under load in the 2026-10-09 full-suite run.

# Steps to Reproduce

1. Build a BufferMemory with a clock that does not advance and `WithPerTenantSessionLimit(2)`.
2. Append to sessions one, two, three of one tenant.
3. Load session one.

# Expected

Session one is evicted; two and three are kept, every time.

# Actual

Roughly one run in ten keeps session one and evicts another.

# Acceptance Criteria
- [x] Recency is ordered by a monotonic counter, not the clock (`touch` in internal/ai/memory.go); `TestPerTenantCeilingEvictsByRecencyWhenTheClockDoesNotMove` (50 runs with a frozen clock) failed before the fix and passes after; `go test -count=200 -run TestPerTenantCeiling ./internal/ai/` passes.
