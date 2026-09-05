---
id: FEAT-sp8cfm
title: Map WAHA workflows through the n8n importer
status: todo
priority: high
labels:
    - nodes
    - waha
    - telegram
    - packs
deps:
    - FEAT-qe6wb8
    - FEAT-bp0ytb
    - FEAT-t5q318
    - FEAT-k3fmj1
parent: EPIC-m42s3g
phase: p3
created: "2026-09-05T05:02:19Z"
updated: "2026-09-05T05:02:19Z"
---

## Scope

The WAHA pack and the WAHA trigger are useless to the stated business goal until the importer knows how to reach them. `internal/interop/n8n` advertises exactly ten mappings today — the `mappings` table in `n8n.go`, matched by exact string in `byN8NType` — and anything outside it becomes `kilasflow.unsupported`, a placeholder whose `Validate` always fails, so a real WAHA template imports as a canvas full of nodes that cannot activate. This ticket adds `@devlikeapro/n8n-nodes-waha.WAHA` and `@devlikeapro/n8n-nodes-waha.wahaTrigger`, plus the legacy unscoped `n8n-nodes-waha.*` forms the older published package used.

Note the capitalisation: one package ships `WAHA` in caps for the action node and `wahaTrigger` in camel case for the trigger. Type strings are matched byte for byte here — the existing table already relies on that, carrying `n8n-nodes-base.mySql` verbatim — and any attempt to be helpful by lower-casing or normalising them will break both mappings at once.

Three things in the current importer stand in the way. Versions: `Import` sets `converted.TypeVersion = entry.kilasVersion`, a single `int` fixed per mapping, so both WAHA versions would collapse onto one definition; WAHA's typeVersion is a YYYYMM integer (`202409`, `202502`) and has to dispatch to the matching registered version. Ports: `outputPortName` resolves an n8n output index through `outputPortsFor`, which returns `{"true","false"}` for `kilasflow.if` and `{"main"}` for everything else — so all twenty of a WAHA trigger's outputs would collapse onto `main` and every branch of an imported template would land on the same wire. Credentials: `Node.Credentials` is decoded off the wire and then never read by `Import`, so today every credential reference is dropped in silence.

Dropping the credential reference is the right instinct and the wrong outcome. An n8n credential is `{id, name}` scoped to the instance it came from; the id means nothing here. But saying nothing leaves the user with a node that looks configured and fails at run time. The import has to name the credential the workflow expects and leave the node visibly unconfigured until it is bound to a local one.

## Acceptance criteria

- [ ] `@devlikeapro/n8n-nodes-waha.WAHA` and `@devlikeapro/n8n-nodes-waha.wahaTrigger` both import onto the native WAHA nodes, and both legacy unscoped `n8n-nodes-waha.*` types map to the same targets.
- [ ] A YYYYMM typeVersion selects the matching registered node version; an unknown version reports which versions exist instead of silently importing at another one.
- [ ] `resource` and `operation` parameter values survive import unchanged and select a real operation in the imported node, proven against the WAHA templates in the import corpus.
- [ ] A WAHA trigger's output indexes map onto that version's event port names, so a template wiring two events to two branches keeps both wires.
- [ ] An imported node that referenced a WAHA credential in n8n reports the credential name it expected and imports unbound; no foreign credential id is ever stored or trusted.
- [ ] Export of a native WAHA node reproduces the original n8n type string, including its capitalisation, and its typeVersion.
- [ ] `SupportedMappings()` lists the new pairs, so the advertised subset stays a written-down claim rather than an inferred one.
- [ ] The corpus measurement is recorded: how many WAHA templates import, how many activate, how many execute, before and after.

## Implementation Plan

Extend the `mapping` struct rather than adding a parallel path. It needs a version-aware target — the current `kilasVersion int` becomes whatever the widened version type ends up being — and a way to say "this n8n type maps to this KilasFlow type at the same version number", since WAHA's versions are shared between the two sides. Add the legacy types as additional entries pointing at the same targets rather than by prefix-matching; an explicit list is greppable and cannot accidentally capture a package that merely starts with the same characters.

Fix `outputPortsFor` before the trigger mapping, not after. Its hardcoded switch is the reason a fan-out trigger cannot round-trip, and the fix is to ask the node registry for the definition's declared output ports instead of guessing from the type string — which also makes `outputIndexesFor`, its inverse used on export, correct for free. The importer currently has no registry reference, so this is a signature change through `Import` and `Export` and their API handlers; do it once, deliberately, rather than threading a WAHA special case through.

For credentials, add a per-node import diagnostic carrying the n8n credential type and the display name from `{id, name}`, and leave `Node.Credentials` empty on the canonical node. The diagnostic is the same channel the unsupported report already uses, so the import screen gets it with no new plumbing.

The trap is testing against invented fixtures. Every claim in this ticket is about matching strings a real template contains, so the tests must read the WAHA templates collected into the import corpus. A fixture written by the same person who wrote the mapping proves only that they were self-consistent.

## References

- Roadmap plan, p3 section, entry V2-p3-5: `/Users/izzadev/.claude/plans/distributed-worker-nats-crispy-finch.md`.
- `internal/interop/n8n/n8n.go` — the `mapping` struct, the `mappings` table, `byN8NType` / `byKilasType`, `Import`'s unsupported branch and its `converted.TypeVersion = entry.kilasVersion`, `outputPortName` / `outputPortsFor` / `inputPortName` / `inputPortsFor`, and `Node.Credentials`, decoded but unused on import.
- `.pine/tickets/FEAT-chxkvq.md` — the V1 interop ticket that set the advertised-subset rule and the unsupported-placeholder contract this extends.
