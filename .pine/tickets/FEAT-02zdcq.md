---
id: FEAT-02zdcq
title: 'Build nodes for your API: choosing a path, OpenAPI-generated packs, credentials for your API, app-fired signed triggers'
status: todo
priority: medium
labels:
    - docs
    - node-authoring
    - packs
parent: EPIC-62zt4j
created: "2026-09-23T02:04:35Z"
updated: "2026-09-23T02:04:35Z"
---

# Description

"Add nodes for my product's API" is the advertised extension path, but OpenAPI generation is undocumented, the scaffold names a credential type that can never exist, the docs contradict each other on custom credential types, and the HMAC signature format for app-fired triggers is undocumented.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: docs-saas; finding ids: DOC-7, DOC-8, DOC-14, DOC-25). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-62zt4j/`.

# Findings

## DOC-7: Docs contradict each other on custom credential types, and the node-pack scaffold names a credential type that can never exist

*docs · high · node-packs*

**n8n:** n8n nodes ship their own credential classes.

**Steps to reproduce:**

1. `nodepackgen scaffold -dir <tmp>/acme`, then `nodepackgen validate` → "packs are valid". The manifest has `"credentialType": "exampleApi"`.
2. Boot a server with `KILASFLOW_PACKS_DIR` pointing at it. `pack.example` registers with `credentials: [{type: exampleApi, required: true}]`.
3. `POST /api/v1/credentials {"type":"exampleApi",…}` → `422 credential type "exampleApi" is not supported`. `POST /workflows/validate` on a workflow that uses the node → blocking `node "p" requires a exampleApi credential`. Reproduced twice (fresh server each time, `evidence/kf-packs-server.log`).
4. The same pack with `"credentialType": "httpHeaderAuth"` and a literal `baseURL` runs and sends the header (verified against a local echo server). That is the working recipe, and it is written down nowhere.

**Actual:**

`guides/node-authoring.md:51-53` says the scaffold "loads and executes without further editing", which is false. Step 4 of that guide invents `acmeApi`. `concepts/credentials.md:117` says "a new credential type needs no Go change", while lines 161-162 and 205 of the same page and `guides/tenant-scoped-nodes.md:155-157` say packs cannot add credential types. The credentials page also has no guidance on which generic type to use for "my product's API".

**Expected:**

One consistent answer. Pack authors use a generic type (`httpHeaderAuth`, `httpBearerAuth`, `httpQueryAuth`, `httpCustomAuth`, `httpBasicAuth`). The base URL either lives in `requestDefaults` or needs a type with a non-secret `baseUrl`. A new named type needs a Go change in `internal/credentials/builtin.go`. The scaffold should default to a generic type.

**Suggested fix:**

Change the scaffold's `credentialType` to `httpHeaderAuth`, and add a "Credentials for your API" page. Longer term, let a pack declare a credential type as data, since `Authentication` is already data.

**Evidence:**

the steps above, and `internal/credentials/registry.go:53-60,108-118`.

**Related:**

FEAT-c81kp3 (todo, generic OAuth2) is adjacent. Nothing else covers this.


## DOC-8: Generating a node pack from OpenAPI, the path the product advertises for "nodes for my API", is undocumented; `nodepackgen -h` hides the author subcommands

*gap · high · node-packs*

**Steps to reproduce:**

1. `grep -rn -e "-spec" -e "-manifest" -e "make node-packs" docs/src/content/docs` finds no hits outside the generated API pages. `concepts/architecture.md:176` is the only mention ("generates a node pack from an OpenAPI document").
2. `go build -o <tmp>/nodepackgen ./cmd/nodepackgen && <tmp>/nodepackgen -h` prints only `-manifest -out -report -spec -trigger-out`. `scaffold`, `validate` and `pack` exist (`cmd/nodepackgen/main.go:39-43`) but are not listed.
3. The generation manifest format (`baseUrl`, `security` mapping scheme to credential type, `parameterDefaults`, `trigger.eventsFrom`) appears only by example in `packs/waha/manifest-202502.json`.
4. The n8n declarative-node converter (`internal/nodepack/convert.go`, `ConvertDocument`) has no CLI entry and no docs. Its only caller is `e2e/fixtures/pack-convert-driver.go`.

**Actual:**

A SaaS integrator with an OpenAPI spec for their own API has no documented path to a pack.

**Expected:**

A "Generate a pack from OpenAPI" guide with a manifest field reference, a worked example on a small spec, how to read the generated `REPORT-*.md`, and how to regenerate. `nodepackgen -h` should list its subcommands.

**Suggested fix:**

Write the guide and add a usage banner. Expose the converter as `nodepackgen convert` or remove it from any claims.

**Evidence:**

the steps above.

**Related:**

FEAT-znm60y (done; the generator shipped without user docs), FEAT-cwz4ac (done)


## DOC-14: The node-authoring guide says `kilasflow pack validate` "refuses the argument"; the verb exists and works

*docs · medium · node-packs*

**Steps to reproduce:**

1. `guides/node-authoring.md:12-14`: "the server binary takes flags only, so `kilasflow pack validate …` refuses the argument rather than doing anything."
2. `bin/kilasflow pack validate $SP/agents/docs-saas/packs2/hostapp` → `{"ok":true,"data":{"path":…,"ok":true,"issues":[]}, …}`. `reference/cli.md` documents the verb.

**Actual:**

Two pages disagree, and the guide is wrong.

**Expected:**

The guide names both tools: `kilasflow pack validate` for validation, and `nodepackgen` for scaffold, pack and generate.

**Suggested fix:**

Edit the paragraph.

**Evidence:**

the steps above.

**Related:**

FEAT-de8d4c, FEAT-bp59m4 (done)


## DOC-25: The HMAC signature format a host must produce for a pack trigger is undocumented

*gap · low · triggers*

**Steps to reproduce:**

1. `guides/node-authoring.md:425-426` and `reference/node-packs.md:67` say only "SHA-512 over the raw body" and "`hmac.algorithm` is `sha512`".
2. `internal/webhook/shape.go:523-541`: the value is lowercase hex of HMAC-SHA512 over the exact raw bytes, with no `sha512=` prefix, no timestamp and therefore no replay window. A missing header is a refusal.

**Actual:**

A host app that fires events into its own pack trigger has to read Go to sign correctly, and is not told that replay protection comes from the dedup window rather than a timestamp.

**Expected:**

A signing recipe in Node and Go, plus the limits (sha512 only, no timestamp).

**Suggested fix:**

Add the recipe to an "App events as triggers" page.

**Evidence:**

the steps above.

**Related:**

none


# Acceptance Criteria
- [ ] A "choosing a path" page covers declarative pack, OpenAPI-generated pack, tenant-scoped node, WASM module and JS sidecar, with a decision table
- [ ] An OpenAPI guide, with a generation-manifest reference, produces a pack that runs; `nodepackgen -h` lists its subcommands
- [ ] The scaffold uses an existing credential type (for example `httpHeaderAuth`) and runs end to end; there is one consistent answer on custom credential types
- [ ] An app-events-as-triggers recipe documents the signature (algorithm, encoding, headers, dedup header, lifecycle template vocabulary) and verifies against a real trigger
- [ ] The node-authoring guide stops claiming `pack validate` is refused
- [ ] The pack guide covers: choosing a credential type today; calling the host API over a private network with `outbound.allowed_private_endpoints`; `deliveryIdHeader` dedup for pack triggers; the lifecycle template vocabulary (Parameter.*, credential fields, Route, PublicURL; scalars only until BUG-vsmnby); upgrading a pack (checksum, restart, version resolution); the `validate` caveat for trigger packs

# Implementation Plan

See each finding's suggested fix above.

# Notes

Also from the host-SaaS integration review (D5).

Related tickets: FEAT-bp59m4, FEAT-c81kp3, FEAT-cwz4ac, FEAT-de8d4c, FEAT-znm60y

# Related Files

# Attachments
