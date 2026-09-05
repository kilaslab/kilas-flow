---
id: BUG-xmcm8x
title: Export drops a mapped node's error handling and the workflow timezone
status: done
priority: high
created: "2026-09-05T17:54:19Z"
updated: "2026-09-05T19:13:10Z"
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

- [x] A node carrying `continueOnFail`, `retryOnFail`, `maxTries` and
      `waitBetweenTries` exports with all four, proven by a fixture that
      imports an n8n workflow and asserts the exported JSON field by field.
- [~] `notes` and `disabled` survive the same round trip, since they are lost
      by the same line and for the same reason. **Wrong as written — see the
      evidence.** They are never imported, so there is nothing to export.
- [x] A workflow whose settings name a timezone exports with that timezone,
      proven by a fixture.
- [x] Anything the exporter still cannot carry is named in `Lossy` rather than
      dropped silently — the existing severity vocabulary already distinguishes
      "carried differently" from "not carried at all".
- [x] `TestExportingASQLWorkflowTwiceIsIdempotent` still passes, and an
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

## Work evidence

Both defects were confirmed by reading the code before anything changed, and
both are fixed at one site each rather than per node type.

`applyErrorHandling` writes the four error settings back onto every mapped
node, and it sits directly beside `errorHandlingSettings`, which reads them in.
The pairing is the point: the two functions share four key names, and a rename
in one that missed the other would lose a retry policy silently — which is the
defect being closed. `exportSettings` writes the workflow timezone, and only
the timezone: keys this server invented are not written back, because a
document carrying settings the receiving system does not understand is how two
formats start to diverge.

### One acceptance criterion was wrong, and I wrote it

The ticket asks that `notes` and `disabled` survive the round trip "since they
are lost by the same line and for the same reason". They are not. The importer
deliberately does not carry either — `nodeSettingIssues` reports both as
dropped, with reasons: n8n's note has no KilasFlow equivalent, and n8n's
disabled flag has none, "so this node will run". Nothing arrives, so nothing
can leave. Exporting them would mean inventing values the canonical document
does not hold.

That is a real gap, but it is a *feature* — carrying a disabled flag means
honouring it in the runner — and it belongs to whoever adds the setting, not
here. Marked as not applicable rather than silently dropped from the list.

### The test shape matters

`TestErrorHandlingSurvivesTheJourneyBackToN8N` goes one hop, from n8n's JSON to
the exported JSON. An export-import-export idempotence check cannot see this
class of defect: it passes when both exports are equally wrong, and both were —
the fields were absent from each, so the two agreed perfectly while losing the
setting.

Proven to catch it. With both changes reverted, all five assertions fail:

```
continueOnFail was lost, so the node fails the whole run on its first error
retryOnFail was lost
maxTries = 0, want 3
waitBetweenTries = 0, want 250
settings.timezone = <nil>, want the zone the workflow arrived with
```

### Runs

```
go test ./... -count=1     green, with live PostgreSQL 16, MySQL 8 and MariaDB 11
go vet ./... ; gofmt -l .  clean
```
