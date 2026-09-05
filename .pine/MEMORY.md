# Project memory

Stable preferences, conventions, and rules for this repository.
Agents: prefer appending here (or a topic under memory/) over creating new LRN-* files.
Ticket-scoped one-shots still use `pine learn --scope ticket`.

## Preferences

## Conventions

## Gotchas

## Log
- 2026-08-29: Product name is KilasFlow; Go module github.com/kilaslabs/kilas-flow; binary/cmd/env use kilasflow / KILASFLOW_.
- 2026-09-05: UI/editor tickets have reference screenshots at design-refs/n8n-v2/ (17 shots, n8n 2.33.7, captured 2026-09-05) and design-refs/n8n/ (V1 set). Each INDEX.md maps every shot to the plan entries it informs — read the INDEX before designing any NDV, node-picker, expression-editor or version-history surface. The directory is gitignored third-party product UI: reference the interaction and information architecture, never copy n8n branding or visual style, and never commit the images. (cites: design-refs/n8n-v2/INDEX.md)
- 2026-09-05: The licence boundary that governs all V2 work is memory/licensing.md — read it before adding any dependency, vendoring any file, or copying anything out of the n8n reference checkout.
- 2026-09-05: Two different things in this repo share the word SDK and the confusion is live in ticket bodies: 'sdk/' is @kilasflow/sdk, the TypeScript HOST SDK a customer's app installs from npm to drive the API (Transport, KilasFlowClient, the iframe embed handshake, orval-generated types); 'pkg/sdk' is an empty placeholder reserved for the guest-side GO module a WebAssembly node-pack author imports (FEAT-48hreg). Neither is a version of the other and they have different audiences. FEAT-48hreg carries an amendment saying so.
- 2026-09-05: Never set outbound.allow_private_networks=true to make a test reach a local service. internal/config defaults it to false and internal/safehttp rejects a resolved loopback address at dial time - the guard working as designed, since a tenant-supplied base URL reaching loopback is the attack it exists to stop. A suite running with that flag on has a security posture production does not have and can never catch a regression in the guard. Use an outbound.allowed_hosts entry for the one endpoint, plus a test proving a DIFFERENT loopback address is still refused. This is what makes local-Ollama AI testing (FEAT-kwxxd0) and the e2e stub server (FEAT-cx3hq1) possible without weakening the product.
- 2026-09-05: The only real type check for web/ is `pnpm check` from web/ (svelte-kit sync && svelte-check --tsconfig ./tsconfig.json). A bare `npx svelte-check --threshold error` silently checks nothing and reports 0 errors over 1300+ files, so it can be used to 'prove' a broken branch is clean. Verify any check command by planting a deliberate type error once in a .ts and once in a .svelte file and confirming both are caught.
