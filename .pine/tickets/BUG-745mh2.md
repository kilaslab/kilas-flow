---
id: BUG-745mh2
title: The node panel probes credentials it never shows, twice for duplicates, before the list has loaded
status: todo
priority: medium
parent: EPIC-8rbys7
created: "2026-10-16T15:30:38Z"
updated: "2026-10-16T15:30:38Z"
---

# Description

Follow-up from the 2026-10-16 review of FEAT-8752vx's deleted-credential flag. The node panel's credential probe (GET /credentials/{id} for ids missing from the picker's list) is wider than the UI that uses it: it probes every id in `node.credentials`, including types the panel does not render (`visibleWhen` false), on the Settings tab, and when the panel cannot change credentials; duplicate ids can be probed twice; and a probe that finds a live credential runs the server's expensive usage scan.

# Acceptance Criteria
- [x] Probe ids come only from the credential types the panel renders, only while the credential section is shown, deduplicated, and not before the credential list has loaded — `credentialIdsToProbe` (`web/src/lib/workflow-editor/credentials.ts`) builds the ids from `applicableCredentialTypes` (visibleWhen) with a Set, and returns none unless the parameters tab is active with `onCredentialChange` present and `credentialsLoaded` is true; `probeCredentials` also dedupes and forwards the query `AbortSignal` to `getCredential`. `credentialsLoaded` is a new prop on the panel and `WorkflowEditor` (default true), set by the dashboard workflow page and `embed-editor.svelte` once the credential listing settles (success or failure), so the panel knows cheaply that an empty list is not yet a loaded one. Live-credential probes (the expensive usage scan) now only happen for a listed-or-unlisted id the section displays, never for hidden types.
- [x] Vitest proves a listed id, an id of a hidden type and a duplicate id are not probed — `credentials.test.ts` ("credentialIdsToProbe": listed id, hidden type, shared id once, section hidden, list not loaded, empty reference; "probeCredentials cancellation") and `properties-panel.test.ts` ("which credentials the panel asks the server about", reading the probe key the panel registers in the query cache); `pnpm check` 0 errors, `pnpm test` 1195 passed.

## Progress (2026-10-16)

Done in one change. The probe set is computed by the pure `credentialIdsToProbe` from the types the panel renders, so it can never be wider than the section it feeds. The "list loaded" question was cheap to answer: both hosts already load the list in `onMount`, so each now holds a `credentialsLoaded` flag set in a `finally` and passes it down through `WorkflowEditor` to the panel. A failed load still counts as settled (the 404 probe is authoritative either way, and a 403/503 answer is `unknown`). Left as is: a live credential still costs the server one usage scan per probe, but only for ids the panel is showing and that the list lacks, deduplicated and cached for the query client's 30 s stale time.
