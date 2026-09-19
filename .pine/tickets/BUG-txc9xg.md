---
id: BUG-txc9xg
title: Set import keeps only assignments; include/raw/fields/options lost
status: todo
priority: critical
labels:
    - importer
    - set
    - n8n-parity
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:09Z"
updated: "2026-09-19T12:06:09Z"
---

Source: KilasFlow full-review workflow `wf_c415e773-4e1` (Find 14/14 + Verify 14/14 + Critique 1/1). Evidence: live repros against stub/n8n/private instances in `scratchpad/work/<dim>/` (FINDINGS.md, PROGRESS.md) plus journal `wf_c415e773-4e1/journal.jsonl`. Excluded from this epic: 10 verifier-refuted/tracked items (documented bounds, already-open FEAT-1axhdn/FEAT-8mymac halves).

Note: Grounded via Context7 n8n docs: Set Manual Mapping + Keep Only Set Fields discards unused input; KF defaults include=all, opposite of n8n 3.3+.

Consolidates 2 finding(s) from dims: find:core-node-parity, find:importer-fidelity.

---
### Set v3 import ignores keep-only/include/raw/options, so Set passes through every input field [find:core-node-parity] (high/parity-gap) · area: importer / Set · confidence: high

setToKilas carries only the assignments. It never maps mode/jsonOutput, includeOtherFields, include/includeFields/excludeFields, keepOnlySet (v1/v2) or options (dotNotation, ignoreConversionErrors), even though kilasflow.set supports all of them. With n8n's default settings a Set outputs only the fields it sets. The imported KilasFlow Set adds them to the whole input item instead, and no import issue says so. The only warning is stale ('no KilasFlow equivalent') and fires only when includeOtherFields:false is written explicitly, which is never the default.

Evidence: Case cases/set.json:
- Set 3.4 {assignments:[greeting]} with no includeOtherFields: n8n [{greeting:'hi Alice'},...]; KilasFlow all 9 input fields plus greeting, and no import issue.
- include=selected, includeFields 'id, city': n8n {id,city,g}; KilasFlow all fields.
- include=except: the excluded fields are kept.
- options.dotNotation=false: n8n {"a.b":"1"}; KilasFlow {a:{b:"1"}}.
- mode=raw with jsonOutput: n8n {myid,up}; KilasFlow passes the item through unchanged (lossy issue 'no readable assignments').
- Set v2 keepOnlySet=true: KilasFlow keeps all fields.
- Case setconv (ignoreConversionErrors=true): n8n keeps the value; KilasFlow fails the run.
Code: internal/interop/n8n/parameters.go:91-150 versus nodes/core.go:101-180.

n8n behavior: Set v3.x defaults to keeping only the set fields. include, raw JSON mode and options behave as configured.

Impact: 167 Set v3 nodes in 54 of the 100 templates use the default keep-only behaviour. Extra fields leak into webhook responses, HTTP bodies and sheet rows. Raw mode is used in 6 templates and keepOnlySet in 3.

Suggested fix: Map includeOtherFields false or absent (v3.x) to include:"none". Map true plus include/includeFields/excludeFields as-is. Map mode raw with jsonOutput, the options, and keepOnlySet to include none. Remove the stale lossy message and add import+run tests.

Files: /Users/izzadev/projects/k-flow/internal/interop/n8n/parameters.go, /Users/izzadev/projects/k-flow/nodes/core.go

Existing tickets: FEAT-jwhdsy (done; AC 29/32 claim raw/include/options mapped with a diagnostic for every unmapped option, so this is a regression), FEAT-nbqye0

---
### Set import keeps only `assignments`: n8n 3.3+ 'only set fields' default, raw/JSON mode, v3.0-3.2 `fields`, include/keepOnlySet and options are lost, so imported Set nodes produce different items [find:importer-fidelity] (critical/bug) · area: importer/Set (n8n-nodes-base.set) · confidence: high

setToKilas (parameters.go:91-149) returns only {assignments}. The native Set executor already supports mode=raw/jsonOutput, include=all|none|selected|except, includeFields/excludeFields and options (nodes/executors.go:206-300), but the importer never fills them, so `include` defaults to all. n8n Set >=3.3 defaults includeOtherFields=false (output only the set fields), so every such imported node leaks every input field downstream. Raw-mode and v3.0-3.2 `fields.values` nodes lose all their fields, and a Set with no fields passes its input through instead of emitting {}. The export side has the inverse problem: setToN8N always writes v3.4 with no include/includeOtherFields/mode.

Evidence: Side-by-side run of the same JSON (Webhook -> Set 3.4 {x:1} -> Set 3.2 fields.values {y} -> Set 3.4 raw {z} -> Set 3.4 {}), with source and execution in work/importer-fidelity/set_probe_src.json and kf_set_probe_exec.json. n8n outputs: Set34 {"x":1}; Set32 {"x":1,"y":"2"}; SetRaw {"z":2}; SetEmpty {}; the webhook responds {}. KilasFlow: every Set outputs the whole webhook item (body, headers, method, path, query) plus x:1; y and z never appear; the webhook responds with the full item, including request headers. Import only raised 'this Set node has no readable assignments' (lossy, typeVersion 0) for 3 of the nodes and nothing about the include default. Template 5170, which activates, run side by side (sbs_5170.json): 8 of its 9 Set nodes emit extra accumulated fields in KilasFlow. Round trip: 25 Set nodes whose source had includeOtherFields:true come back without it (e.g. 1534 'Create su

n8n behavior: v1/v2: keepOnlySet (default false, so input fields are kept). v3.0-3.2: fields.values[] with <type>Value keys, include defaults to all. v3.3+: includeOtherFields defaults to false; include (all/selected/except) with includeFields/excludeFields applies only when it is true. mode raw uses jsonOutput. An empty Set emits {}.

Impact: 56/100 templates have a Set >=3.3 without includeOtherFields, so the extra fields leak into HTTP bodies, AI prompts and webhook responses. Raw Sets lose their JSON in 2320, 2878, 3770, 4110, 4352 and 5035. fields.values is lost in 2035, 2063 and 2462. include selected/except is lost in 2006 and 2315. v1 keepOnlySet:true is ignored in 1744, 1750 and 1751 (1744 and 1750 are 2 of the only 4 activatab

Suggested fix: Version-aware translation: map mode/jsonOutput and v3.0-3.2 fields.values. Map includeOtherFields/include/keepOnlySet, defaulting to include:none for >=3.3 when the key is absent. Map includeFields/excludeFields and options. Export the inverse at the source typeVersion. Add a corpus test per Set version and a side-by-side fixture.

Files: internal/interop/n8n/parameters.go, nodes/executors.go

Existing tickets: FEAT-jwhdsy (done: claims Set raw/include mapping and named diagnostics for every unmapped option; regression)

## Acceptance criteria

- [ ] Set v3 import ignores keep-only/include/raw/options, so Set passes through every input field
- [ ] Set import keeps only `assignments`: n8n 3.3+ 'only set fields' default, raw/JSON mode, v3.0-3.2 `fields`, inc
- [ ] Adversarial re-verify against live stub/n8n like the Verify phase (no code-only close)