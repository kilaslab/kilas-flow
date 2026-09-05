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
- 2026-09-05: CORRECTION to the 2026-09-05 note about allowed_hosts and loopback: an outbound.allowed_hosts entry does NOT let a request reach a private or loopback address. The two guards are independent and neither consults the other. Policy.CheckURL reads AllowedHosts at pre-flight and never looks at an IP; Policy.CheckAddress runs inside NewClient's DialContext on every connection and returns early only on Policy.AllowPrivateNetworks, never reading AllowedHosts. So the only lever for a local Ollama at 127.0.0.1:11434, or any RFC1918 target, is allow_private_networks. This answers the open question in the roadmap's V2-p11-2 entry about whether the allowlist is consulted before or after the private-address guard: it is neither, so FEAT-kwxxd0 is a code ticket rather than a configuration one - safehttp needs a way to say 'this exact host and port may resolve privately' before a local model is reachable without weakening the guard for everything else. Verified 2026-09-05 against internal/safehttp/safehttp.go while doing FEAT-mvegj5; every loopback test in the repo sets AllowPrivateNetworks=true (nodes.localPolicy), which is why nobody had hit this.
