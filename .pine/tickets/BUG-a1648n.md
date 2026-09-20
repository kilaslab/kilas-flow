---
id: BUG-a1648n
title: Editor coerces imported n8n-v2 gt/gte/lt/lte conditions to equals on read, silently rewriting comparisons
status: testing
priority: medium
created: "2026-09-20T01:17:16Z"
updated: "2026-09-20T01:22:54Z"
---

# Description

# Steps to Reproduce

# Expected

# Actual

# Acceptance Criteria
- [ ] Define acceptance criteria

# Related Files

# Attachments

## Work (WebFormsOps 2026-09-20)
- Root cause: KNOWN_OPERATIONS derived only from the editor's own ConditionOperator union (larger/smaller family) — n8n-v2 gt/gte/lt/lte fell through to the 'equals' default in operatorOf, so read+save rewrote the comparison.
- Fix: canonical ConditionOperator is now gt/gte/lt/lte (matches Go evaluator's numberOperation output vocabulary); legacy larger/largerEqual/smaller/smallerEqual folded via OPERATOR_ALIASES at the read boundary; dropdown offers canonical names with the same labels; typeForOperation coerces the type family when the operator changes (gt→number, after→dateTime, true→boolean).
- Tests: 2 new round-trip tests (v2 names survive read+write; legacy names fold to canonical). Pre-fix: 2 failed / 15 passed. Post-fix: 17 passed.
- Scope: web only. Flat-shape server path (readFilter in nodes/conditions.go) untouched — Go executor already folds spellings at comparison time.
