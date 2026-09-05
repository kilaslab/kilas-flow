---
id: EPIC-m42s3g
title: KilasFlow V2 — n8n-first workflow compatibility
status: todo
priority: high
labels:
    - roadmap
    - v2
    - n8n
    - interop
phase: p0
created: "2026-09-05T04:52:29Z"
updated: "2026-09-05T04:52:29Z"
---

## Objective

Make a customer's existing n8n workflows import into KilasFlow and actually run, so client automations — above all ones built on WAHA (WhatsApp HTTP API) — can be replicated on this platform. V1 proved the engine, the editor and the embed boundary. V2 makes them compatible with the ecosystem the customers are coming from.

## Decisions that shape every child ticket

| Decision | Answer |
| --- | --- |
| JS sidecar for community nodes | Deferred to p8. WAHA goes native through an OpenAPI-to-node generator. |
| First phase | Engine correctness and import fidelity, before node metadata. |
| Licence posture | Native-first. No n8n bytes in this repo or in any artifact. No dependency on any n8n npm package. |
| `n8n-nodes-base` coverage | Top-30 by real usage data, not exhaustive parity. |
| Workflow versioning | DB-stored workflow history, not Git source control. |
| PostgreSQL | Optional. It unlocks extra features and can share a customer database under a `kflow_` table prefix. |
| AI nodes | Native Go, mapped on import. Microsoft Agent Framework stays an optional adapter behind `ai.AgentRuntime`, admitted only by spike. |
| Datastore storage | One physical table per datastore, created by runtime DDL under the `kflow_` prefix, alongside a catalogue — matching n8n. Byte quotas are deliberately not a requirement; row, column and datastore counts are. This is the one sanctioned exception to the rule that the internal schema is created by migration. |
| Database node scope | Full n8n operation parity — Delete, Execute Query, Insert, Insert or Update, Select, Update — not raw SQL only. |
| Documentation | A public site on Astro Starlight in `docs/`, dark-first to match the product, with the API reference generated from a real binary's OpenAPI document. Separate from the SvelteKit app in `web/`, which is embedded in the binary. |
| Community node authoring | Declarative packs, no code — the format the WAHA and Telegram packs already use — installed from an operator-configured directory rather than by rebuilding. Plus a converter for the declarative subset of n8n community nodes, gated on a recorded licence position. The WASM path stays where it is, at p8-5. |
| Distribution | Multi-architecture images published to a registry from a real CI pipeline, and a Compose quickstart with a PostgreSQL profile that pulls rather than builds. No Helm chart. |
| End-to-end verification | A Playwright suite against a real binary, a real database and the embedded SPA, with a local Ollama model standing in for a hosted LLM so the AI paths are testable offline. Stubbed suites run often; the epic's own acceptance scenario runs rarely against real third parties. |

## Why the roadmap is ordered this way

Research across the KilasFlow source, a local n8n 2.34.0 reference checkout, the owner's own published `n8n-nodes-mitrachat` package and official documentation overturned four assumptions the obvious plan would have rested on.

- **WAHA needs no JavaScript.** `@devlikeapro/n8n-nodes-waha`'s action node has no `execute()` at all. It is 124 OpenAPI-derived operations of declarative `routing` metadata generated from WAHA's own MIT `openapi.json`, so a Go routing interpreter plus a generated node pack replicates the whole package with the single binary intact.
- **A JS sidecar would run 0.46% of what n8n users run.** Across the 100 most-viewed n8n.io templates (2,377 node instances): 1,894 `n8n-nodes-base`, 472 `@n8n/n8n-nodes-langchain`, 11 third-party `n8n-nodes-*`.
- **The licence boundary is real.** `n8n-workflow`, `n8n-core`, `n8n-nodes-base` and the LangChain pack are all Sustainable Use License. This repository is Apache-2.0 and the product is white-label, multi-tenant and embedded — the configuration n8n's licensing FAQ names as not allowed. Even MIT-licensed WAHA imports `VersionedNodeType` and `NodeConnectionType` from `n8n-workflow` as runtime values, so executing the npm package would drag SUL code in.
- **The engine, not the node catalogue, is the bottleneck.** The compiler rejects cycles and demands exactly one trigger root, which fails 52 of those 100 templates before node types matter, and 511 of 1,617 expressions use `$('Node').item`, which needs pairedItem lineage the runner does not track.

## Phase order

p0 reference and guardrails → p1 engine correctness and import fidelity → p2 node metadata foundation → p3 declarative node packs, WAHA and Telegram → p4 n8n-core node parity → p5 AI parity in native Go → p6 PostgreSQL capability tier → p7 workflow history → p8 platform and long tail → p9 Datastore → p10 distribution, SDK and documentation → p11 end-to-end acceptance.

A phase number expresses dependency depth, not a serial queue. `pine ready` is driven purely by `deps`, so a later-numbered ticket becomes workable the moment the specific tickets it names are done — p9 does not wait for all of p8, and the p4 database family does not wait for p3.

## Acceptance scenario

Two proofs, in this order.

1. **Telegram, after p3.** A Telegram Trigger that registers its own webhook, an AI Agent, and a Send Message reply — a working bot without any WhatsApp infrastructure.
2. **WAHA, the real target.** The official WAHA chatting template imports, opens in the editor with correct icons and parameter panels, activates, receives a real webhook and replies — with the same template imported twice for two different tenants, both active at once.

3. **Datastore, the storage proof.** A datastore created through the API, its columns edited in the editor, rows written and read by a workflow node, the same workflow imported from an n8n export that used a Data Table, and a second tenant proven unable to read the first one's rows.

4. **The external consumer, the product proof.** An application outside this repository uses KilasFlow with no checkout of it: `docker run` the published image, `npm install @kilasflow/sdk`, create a workflow and a datastore through the SDK, mount the embedded editor in a host page, import an n8n workflow through the editor rather than through `curl`, and install a community node pack by placing it in a directory.

All four must run with no Node.js process anywhere. p11 makes this scenario executable rather than a judgement call: `FEAT-5fhj6p` runs all four proofs against the published artefacts.

## Delivery rules carried over from V1

- API first. The editor never owns authoritative workflow state.
- A draft may be incomplete; the compiler stays the single validation authority for activation and execution.
- Workflow JSON carries credential references only, never plaintext secrets.
- Persist execution evidence from the first runnable slice of every phase.
- One commit per ticket, carrying its own code, tests and evidence. Close with `pine close <ID> --evidence`.

## References

- Roadmap plan: `.pine/roadmap.md`.
- PRD: `gflow-prd-v1.md` §§19–24, 42, 54, 56–58, 64.
- V1 epic: EPIC-c7gbdp.
- Licence boundary: `.pine/memory/licensing.md` (written by the p0 guardrails ticket).
