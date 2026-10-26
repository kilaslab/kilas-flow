---
title: White-label, localisation and go-live
description: What a host can and cannot change about the embedded editor, which locales ship and how one is added, and the two ways an embedded user's workflow goes live.
---

The embedded editor is the only surface a host's end users see, and this page
is the whole of what a host can change about it: the branding values a session
carries, the language the chrome renders in, and the authority to put a
workflow live. The mechanisms behind each — session minting, scopes, the
postMessage protocol — are covered in
[Embedding and multi-tenancy](/guides/embedding/); this page states the
white-label surface itself, including what it deliberately is not.

## Branding: what a session may set

Branding travels with the minted session and the editor renders it into
elements it controls. There is deliberately no way to pass CSS, HTML, or a
script. The fields, with their validation, are tabulated in
[Branding: values, never markup](/guides/embedding/#branding-values-never-markup):
`name`, `logoUrl`, `accent`, `hideRun` and `hideSave` — five fields, and the
server validates every one at mint before the editor ever sees it.

An operator can set deployment-wide defaults with `branding.name` and
`branding.logo` (`KILASFLOW_BRANDING_NAME` / `KILASFLOW_BRANDING_LOGO`); a
session's values win field by field, and an empty value cannot blank a
deployment default. A deployment hosting more than one brand leaves both
empty and passes everything per session. The defaults reach only the embedded
editor — the operator dashboard keeps its own name, logo and icon. There is
no per-tenant branding stored server-side: the tenant row carries no branding,
so the host's backend is the place a "which brand does this customer get"
lookup lives.

`accent` is one colour and the editor derives every accent use from it — the
primary button with its hover and pressed shades, links, the focus ring, node
and row selection — with the text on an accent-filled fill flipping between
near-black ink and white by the accent's lightness. Status colours (success,
warning, error, running) never take the accent.

## What cannot be customised

Stated plainly, because a host planning white-label work needs the list:

- **The theme.** The embedded editor is dark, full stop. The product's
  stylesheet carries a light palette, but nothing in the embed surface can
  select it — `MountOptions` in the SDK has no theme field, and the embed
  shell applies no `data-theme` — so a light-first host page frames a dark
  editor. Theme control (`branding.theme`) is planned, not shipped.
- **Fonts, corner radius, spacing, any stylesheet token.** There is no
  `fontFamily`, `radius` or token override; the editor's typography and
  geometry are its own.
- **The chrome itself.** The embed shell has no sidebar, no workspace header
  and no navigation to any other page — a host page cannot expose the
  dashboard's surfaces, and cannot add to the editor's own.
- **Markup of any kind.** Branding is a validated value set, never markup:
  the fields are the five above and no others.

### `hideRun` and `hideSave`, exactly

Both are presentation only, and each hides exactly one control in the embed
header: `hideRun` the run control, `hideSave` the save control. Two things
they are not:

- They are not authority. The server enforces scopes regardless: a session
  without `workflow:run` cannot run a workflow whether or not the button is
  visible, and a session with the scope runs it from a host-side command even
  with the button hidden.
- `hideRun` does not override the session. The editor itself hides the run
  control whenever the session lacks `workflow:run`, so the visible control
  is `hideRun || !canRun` — hiding it for a session that may run is the
  host's choice; hiding it for one that may not is automatic.

## Localisation

The editor's chrome — buttons, panels, errors — is localised, and the language
is the host's choice at mount:

```js
import { mountWorkflowEditor } from '@kilasflow/sdk/browser';

const editor = mountWorkflowEditor({
  container, baseUrl, session,
  locale: 'id' // the editor's chrome and <html lang> follow it
});
```

**The shipped locales are `en` (the base locale) and `id`.** The list lives in
`web/project.inlang/settings.json` — `"locales": ["en", "id"]` — and the
catalogs in `web/messages/<locale>/`. The frame checks the tag it is handed
against the compiled catalog list (the paraglide runtime's own `isLocale`), so
a language this build does not carry is ignored and the editor stays in its
base locale rather than failing the handshake: naming a language the
deployment does not have cannot break the host's page. A given locale also
outranks whatever language a reader previously picked on the editor's own
origin — the host decides how its page reads.

### Adding a locale

A locale is a build artefact, not a runtime upload. To add one:

1. Add the tag to `locales` in `web/project.inlang/settings.json`.
2. Create `web/messages/<tag>/` with a translation of every catalog file
   `en/` carries (`common.json`, `nav.json`, `workflows.json`,
   `executions.json`, `schedules.json`, `credentials.json`, `datastores.json`,
   `settings.json`, `auth.json`, `home.json`, `editor.json`, `canvas.json`,
   `properties.json`, `versions.json`, `embed.json`).
3. Compile and rebuild: `pnpm i18n:compile` in `web/` regenerates
   `src/lib/paraglide` from the catalogs, and a deployment serves the new
   language only after the SPA is rebuilt and the binary re-embedded (the
   editor the host frames is served by the kilasflow binary, not from
   `web/`'s dev server).

Until the deployment is rebuilt, a host passing the new tag gets the base
locale, silently — the same fallback as any unknown tag. A locale contributed
to the repository ships to every deployment; there is no per-deployment
locale install.

## Go-live: putting an embedded user's workflow into production

Activation and publishing are owner actions by default — they change what
production traffic runs — and there are exactly two ways a host's user reaches
them. Both call the same handlers the operator dashboard's switch does; a
workflow that uses a node the tenant may not use is refused with the same
`node.not_available` problem either way.

### From the host backend (the tenant key)

The host's backend holds the tenant's `kfa1_…` key and acts on the user's
behalf — a "Go live" button in the host UI calls the host backend, the backend
calls KilasFlow:

```js
// Pin a saved revision as the version production traffic runs…
await tenant.client.publishWorkflowVersion(workflowId, versionId, 'customer requested');
// …and turn the workflow on (or off with deactivateWorkflow).
await tenant.client.activateWorkflow(workflowId);
```

This is the model for a host that keeps go-live as a reviewed step: the
session the user edits in carries read/write/run only, and the button's
appearance in the host UI is the host's own entitlement check.

### From the embedded editor (minted scopes)

A host that wants its customers to go live without a round trip mints the
session with the two scopes on purpose — `workflow:publish` to pin a named
revision in the version panel, `workflow:activate` for the activate/deactivate
switch. Neither is implied by `workflow:write`; both imply `workflow:read`;
no scoped API key can be minted with either (see
[publish and activate authority](/guides/embedding/#publish-and-activate-authority)
for the full table). Refusals name the reason — "This embed session cannot
publish a revision without the workflow:publish scope." — because the caller
is the host's own page.

The frame tells the host page when it happened: `workflow-published
{ versionId, revision }` on the event channel, so a host UI can update its own
state beside the editor rather than polling.

## Not shipped yet

Listed here rather than omitted, per this site's rule:

- **Theme selection for the embed** (`branding.theme`, light/dark/system),
  **`branding.fontFamily` and `branding.radius`** — the branding surface is
  the five fields above. A light palette exists in the product's stylesheet;
  nothing in the embed can select it.
- **Per-tenant branding stored server-side** — deployment defaults are
  global; per-customer branding is passed per session by the host.
- **Runtime locale install** — adding a locale is a rebuild (above), and
  per-deployment catalogs do not exist.
