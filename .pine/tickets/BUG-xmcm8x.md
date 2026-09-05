---
id: BUG-xmcm8x
title: Export drops a mapped node's error handling and the workflow timezone
status: todo
priority: high
created: "2026-09-05T17:54:19Z"
updated: "2026-09-05T17:54:19Z"
---

# Description

# Steps to Reproduce

# Expected

# Actual

# Acceptance Criteria
- [ ] Define acceptance criteria

# Related Files

# Attachments

## Scope

Two things the exporter drops without saying so. Both were found while
fact-checking the n8n migration guide (FEAT-zmfsjd), and both are confirmed by
reading the current code rather than inferred.

**A mapped node loses its error handling.** `Import` carries n8n's
`continueOnFail`, `retryOnFail`, `maxTries` and `waitBetweenTries` onto the
canonical node — `errorHandlingSettings` at `internal/interop/n8n/n8n.go:1440`
does the work, and the runner honours them. `Export` then builds a mapped node
from four fields only:

```go
exported := Node{
    ID: node.ID, Name: node.Name, Type: entry.n8nType, TypeVersion: exportVersion(entry, node),
    Position: []float64{node.Position.X, node.Position.Y},
}
```

No error handling, no notes, no disabled flag — and no `Lossy` issue naming
what went missing. The asymmetry is the sharp part: a node with **no** n8n
equivalent round-trips faithfully, because the capsule keeps everything, while
a node this server supports comes back having quietly lost its retry policy. A
workflow whose HTTP node was configured to retry three times and continue on
failure returns to n8n as one that fails the whole run on the first error.

**The workflow timezone is never written back.** `Export` initialises
`Settings: map[string]any{}` (`internal/interop/n8n/n8n.go:1004`) and nothing
ever fills it. Import deliberately carries the timezone across — a dropped zone
runs every schedule at the wrong hour, every day, which is the failure that
motivated carrying it in the first place — and the export throws it away
undiagnosed.

## Acceptance criteria

- [ ] A node carrying `continueOnFail`, `retryOnFail`, `maxTries` and
      `waitBetweenTries` exports with all four, proven by a fixture that
      imports an n8n workflow and asserts the exported JSON field by field.
- [ ] `notes` and `disabled` survive the same round trip, since they are lost
      by the same line and for the same reason.
- [ ] A workflow whose settings name a timezone exports with that timezone,
      proven by a fixture.
- [ ] Anything the exporter still cannot carry is named in `Lossy` rather than
      dropped silently — the existing severity vocabulary already distinguishes
      "carried differently" from "not carried at all".
- [ ] `TestExportingASQLWorkflowTwiceIsIdempotent` still passes, and an
      equivalent idempotence assertion covers the settings block.

## Implementation Plan

Fix the export at the one site rather than per node type: every mapped node
goes through the same construction, so the fields belong there beside the
position. Read them back from wherever `errorHandlingSettings` wrote them so
the two functions cannot disagree about the key names — that pairing is the
whole reason the import side is a named function rather than inline.

For the timezone, mirror whatever `Import` reads. Do not invent a second
spelling: n8n stores it in the workflow's own settings object, and the
importer already knows which key.

**Write the round-trip test as one hop plus a comparison, not two hops.** An
export-import-export idempotence check passes when both exports are equally
wrong, which is exactly the failure here — the field is absent from both. The
assertion has to start from n8n JSON that carries the field and end at exported
JSON that still carries it.

## References

- `internal/interop/n8n/n8n.go` — `Export`'s mapped-node construction, the
  empty `Settings` initialiser, and `errorHandlingSettings` on the import side.
- `internal/interop/n8n/n8n_test.go` — `TestExportNamesWhatItCannotCarry`, which
  is where a "named rather than dropped" assertion belongs.
- FEAT-zmfsjd's work evidence, which recorded both defects while verifying the
  migration guide against the code.
