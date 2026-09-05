---
id: FEAT-pd3p6x
title: Bring conditional property visibility to n8n parity
status: todo
priority: high
labels:
    - registry
    - metadata
    - editor
deps:
    - FEAT-5s1w0t
parent: EPIC-m42s3g
phase: p2
created: "2026-09-05T05:01:27Z"
updated: "2026-09-05T05:01:27Z"
---

## Scope

KilasFlow's conditional visibility is `VisibilityCondition{Key string, Equals any}` in `internal/node/registry.go`, evaluated in exactly one place — `web/src/lib/components/workflow-editor/properties-panel.svelte`, where `isVisible` is `(property.visibleWhen ?? []).every((condition) => current[condition.key] === condition.equals)`. That is single-value, AND-only, show-only, strict-JavaScript-equality matching. It cannot express "show when operation is one of send, sendPhoto or sendDocument", it cannot express "hide when authentication is none", it cannot gate on type version, and its `===` never matches an object or array by value — so a condition on anything but a primitive is silently always false.

n8n's semantics, from `packages/workflow/src/node-helpers.ts` in the 2.34.0 reference, are precise and worth copying exactly. `displayParameter` reads `show` and `hide`. Under `show`, **every** listed key must match, and within one key the listed values are OR'd (`checkConditions` is `conditions.some(...)`). Under `hide`, **any** matching key hides the property — but only when the controlling parameter actually has a value; `values.length !== 0 && checkConditions(...)` means an absent value never hides. `show` also accepts the pseudo-keys `@version`, `@feature` and `@tool`, and values may be condition objects (`{_cnd: {eq | not | gt | gte | lt | lte | between | startsWith | endsWith | includes | regex | exists}}`) rather than bare literals.

Then there is the escape hatch that is easy to miss and expensive to omit: inside the `show` loop, before any comparison, `if (values.some((v) => typeof v === 'string' && v.charAt(0) === '=')) return true;`. If the controlling parameter holds an expression, the dependent parameter is **always shown**, because n8n cannot know at edit time what the expression will evaluate to. It is in the `show` branch only — `hide` has no equivalent. KilasFlow's expression marker is the object `{"mode":"expression","value":…}`, not a leading `=`, so the translation is "the controlling value is an expression marker", but the rule itself carries over unchanged.

There is a second, larger defect this ticket must fix. Visibility exists only on the client. `requiredParameters` in `internal/node/registry.go` collects every `Required` property with no default, and the compiler enforces all of them regardless of whether they are visible. A node shaped like a real n8n node — where `chatId` is required only when `resource` is `message` — is therefore unactivatable in every other configuration. Nothing in p3 can ship until required-ness is conditional, which means the visibility rule needs a Go implementation and the compiler must be its authority.

The plan settles one question that would otherwise be argued during review: **a hidden parent does not suppress its children.** Every property's visibility is evaluated independently against the node's stored parameters, so a property whose controlling parameter is itself hidden is still evaluated on that parameter's stored value. Imported nodes depend on this — n8n behaves the same way, and a cascade would hide parameters that an imported workflow legitimately sets.

## Acceptance criteria

- [ ] The visibility model supports `show` and `hide` groups, multiple keys per group, multiple accepted values per key, and comparison operators beyond equality, replacing `VisibilityCondition{Key, Equals}`.
- [ ] `show` requires every listed key to match with values OR'd within a key; `hide` hides when any listed key matches and never hides on an absent value.
- [ ] A controlling parameter holding a KilasFlow expression marker forces the dependent property to be shown, in the `show` path only, proven by a test.
- [ ] Visibility is evaluated in Go and the compiler treats a hidden required parameter as not required, so a node configured into a branch that hides a required field activates successfully.
- [ ] Visibility gates on type version, so one definition can present different parameters per version.
- [ ] Go and TypeScript evaluate the same shared fixture file and agree on every case, including value coercion between a JSON number and its string form.
- [ ] The settled rule that a hidden parent does not suppress its children is documented on the type and covered by a test.

## Implementation Plan

Write the Go evaluator first, in `internal/node`, as a pure function over `(condition set, parameters map[string]any, typeVersion)`. Wire it into `requiredParameters` — or, more accurately, replace `requiredParameters` with something that takes the node's parameters, since required-ness is no longer a static property of the definition. That ripples into `workflow.NodeDefinition.RequiredParameters` in `internal/workflow/compiler.go`, which is a `[]string` computed at `Lookup` time before any node instance is in hand; it has to become a callback or move the check into the compiler's per-node pass. Recommend the callback: `Lookup` already carries a `Validate` func for the same reason, so the shape is established.

Then port the same rule to `properties-panel.svelte`. Two implementations of one rule will drift — that is not a risk, it is a certainty — so make them read the same evidence. Recommend a JSON fixture under `internal/node/testdata/` enumerating condition sets, parameter maps and expected visibility, consumed by a Go table test and by a vitest case that imports the same file. That is cheap to write once and it is the only thing that keeps the panel and the compiler agreeing about whether a workflow can be saved.

Value comparison needs an explicit decision, because Go and JSON disagree with JavaScript. A number arriving through `encoding/json` is a `float64`; a condition written as `Equals: 1` in Go is an `int`. Recommend comparing after a documented normalisation — numbers compared as `float64`, everything else by `reflect.DeepEqual` — and stating in the doc comment that a condition value is compared by value, not by identity, which is precisely what the current `===` gets wrong.

Do not implement `@feature` or `@tool`. `@version` is needed now, for p1-11's float type versions and for nodes that change shape between versions; the other two gate on n8n instance features that have no KilasFlow equivalent, and adding them would mean inventing semantics. Reject them at registration with a named error rather than accepting and ignoring them.

Migrating the five existing `VisibleWhen` uses — `nodes/webhook.go`, `nodes/database.go` (three), `nodes/http.go` (four), `nodes/core.go` — is mechanical and belongs in this ticket, not left for later.

## References

- Plan: `/Users/izzadev/.claude/plans/distributed-worker-nats-crispy-finch.md`, section "p2 — Node metadata foundation", entry V2-p2-3.
- `internal/node/registry.go` — `VisibilityCondition`, `requiredParameters`.
- `internal/workflow/compiler.go` — `NodeDefinition.RequiredParameters`, `ConfigValidator`.
- `web/src/lib/components/workflow-editor/properties-panel.svelte` — `isVisible`.
- `nodes/webhook.go`, `nodes/database.go`, `nodes/http.go`, `nodes/core.go` — the existing `VisibleWhen` uses.
- n8n 2.34.0 reference (read-only, outside this repo): `packages/workflow/src/node-helpers.ts` — `displayParameter` and `checkConditions`; `packages/workflow/src/interfaces.ts` — `IDisplayOptions` and `DisplayCondition`.
- Local n8n UI reference: `design-refs/n8n-v2/INDEX.md` entry 13 — the resource→operation cascade driving which fields appear below it. Captured from a local n8n 2.33.7 instance; gitignored, never vendored.
