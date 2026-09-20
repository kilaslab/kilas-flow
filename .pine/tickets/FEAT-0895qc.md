---
id: FEAT-0895qc
title: 'Shell workflows/import surface: delete/rename/duplicate, search/sort/page, dialogs, bulk, errors'
status: testing
priority: medium
labels:
    - ui
    - importer
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:10Z"
updated: "2026-09-19T14:53:53Z"
---

Source: KilasFlow full-review workflow `wf_c415e773-4e1` (Find 14/14 + Verify 14/14 + Critique 1/1). Evidence: live repros against stub/n8n/private instances in `scratchpad/work/<dim>/` (FINDINGS.md, PROGRESS.md) plus journal `wf_c415e773-4e1/journal.jsonl`. Excluded from this epic: 10 verifier-refuted/tracked items (documented bounds, already-open FEAT-1axhdn/FEAT-8mymac halves).

Consolidates 19 finding(s) from dims: find:ui-shell-lists.

---
### Workflows cannot be deleted, renamed or duplicated anywhere in the UI [find:ui-shell-lists] (high/parity-gap) · area: workflows list / lifecycle · confidence: high

Each row on /app/workflows is a link plus an Activate/Deactivate button. There is no row menu. The editor shows the workflow name as static truncated text that cannot be edited. The API supports DELETE /api/v1/workflows/{id} and renaming through PUT, but no Svelte file calls deleteWorkflow, and no rename or duplicate UI exists.

Evidence: `grep -rn deleteWorkflow web/src | grep -v api/generated` returns nothing, and no Duplicate/Rename strings exist outside datastore/column code. Row markup: +page.svelte:191-222. Read-only name in the breadcrumb: [id]/+page.svelte:224-230. The shared instance has built up 500+ workflows that cannot be cleaned up from the UI.

Impact: Affects every user. Imported and test workflows can only be removed or renamed through the API.

Suggested fix: Add a row overflow menu with Rename, Duplicate, Export, and Delete (confirm that names the workflow and warns if it is active). Make the workflow name inline-editable in the editor header.

Files: /Users/izzadev/projects/k-flow/web/src/routes/(dashboard)/app/workflows/+page.svelte, /Users/izzadev/projects/k-flow/web/src/routes/(dashboard)/app/workflows/[id]/+page.svelte

---
### Workflows list has no search, sort, filter or pagination, and the API returns every workflow in one response [find:ui-shell-lists] (high/ux) · area: workflows list · confidence: high

With 476 workflows the page rendered 476 rows and the document was 21,561px tall. There is no search box, sort control, active/draft filter or pager. GET /api/v1/workflows takes no query parameters and returned all summaries (88 KB) in one response, while executions and datastore rows are cursor-paged. Row metadata is also thin: updatedAt shows month and day only ('Sep 17', no year or time), with no created date, no trigger type, tags or owner, and just a bare 'r1'.

Evidence: run-code on /app/workflows at 1440px: rows=476, scrollHeight=21561, no input or select on the page. The openapi list-workflows operation has no parameters. Screenshot: /private/tmp/claude-501/-Users-izzadev-projects-k-flow/21032eb5-98ed-492e-a372-bbee00f55df0/scratchpad/work/ui-shell-lists/01-workflows-1440.png. formatUpdatedAt is at +page.svelte:108-111.

Impact: Any workspace with more than about 30 workflows, for example after importing a batch of templates, becomes hard to navigate. It also scales poorly on the server.

Suggested fix: Add name search, an active/draft filter and sorting (updated, created, name) on the client now. Then add server-side limit/cursor/q parameters to list-workflows and show relative updated and created dates.

Files: /Users/izzadev/projects/k-flow/web/src/routes/(dashboard)/app/workflows/+page.svelte, /Users/izzadev/projects/k-flow/internal/api/handlers/workflows.go

---
### Pasting an n8n export into the Import dialog pushes the Import button off-screen, and the dialog cannot scroll [find:ui-shell-lists] (high/bug) · area: import dialog · confidence: high

The textarea grows with its content (field-sizing-content, no max height). Before a result exists, the dialog content only has sm:max-w-lg, with no max-height or overflow-y-auto. Pasting a pretty-printed export grows the dialog far past the viewport. The dialog stays centred, so the title and the Import/Cancel buttons both end up off-screen, and they cannot be scrolled into view (overflow is visible and body scroll is locked).

Evidence: 1440x900, pasted templates/2384.json (5 nodes): dialog height 3175, dialog top -1137, Import button top 1989 with a viewport height of 900, overflowY visible, textarea 2818px. Screenshots in /private/tmp/claude-501/-Users-izzadev-projects-k-flow/21032eb5-98ed-492e-a372-bbee00f55df0/scratchpad/work/ui-shell-lists/: 07-paste-overflow.png (template 1750) and 08-paste-2384-overflow.png. The only way to submit was to focus the name field and press Enter; the first run's Playwright click timed out with 'element is outside of the viewport'. Code: import-dialog.svelte:131-134,178-185 and ui/textarea/textarea.svelte (field-sizing-content).

Impact: Import by paste cannot be completed with a mouse for any non-trivial workflow. FEAT-0556ck promised import 'by file upload and by paste'.

Suggested fix: Cap the textarea height (for example max-h-48 with overflow-auto) and give the form-state dialog max-h-[85vh] overflow-y-auto, as the result state already has. Ideally also accept a JSON paste or drop directly on the list or canvas.

Files: /Users/izzadev/projects/k-flow/web/src/routes/(dashboard)/app/workflows/import-dialog.svelte, /Users/izzadev/projects/k-flow/web/src/lib/components/ui/textarea/textarea.svelte

Existing tickets: FEAT-0556ck

---
### Import diagnostics cannot be acted on: no jump to the node, unreadable truncated cells, no fix path, and the report is lost once the dialog closes [find:ui-shell-lists] (high/ux) · area: import report / diagnostics · confidence: high

The import report fails to support fixing the problems it lists, in five ways.
(a) The verdict says 'Each one names the node to open below', but node cells are plain text with no link into the editor at that node. The editor has no deep-link parameter at all; no code in web/src uses searchParams.
(b) Node, source type and field cells are truncated (max-w-40/48) with no title attribute, so text like 'When chat messag…' or '@n8n/n8n-nodes-langchain…' cannot be read.
(c) Diagnostics are not stored. WorkflowResource has no import-issues field, so after Close, Escape or 'Open in the editor' the lossy and dropped entries (onError dropped, unbound credentials, jsCode, executeOnce) are gone for good. Only the placeholder nodes remain visible on the canvas.
(d) Template 3442 (51 nodes) produced 51 flat rows (23 blocking, 13 lossy, 15 dropped, including 6 'notes' and 5 'webhookId' noise rows), with no grouping by node and no filter. The report also has two identical primary 'Open … in the editor' buttons.
(e) Rows state the problem but never how to fix it. Placeholders say 'cannot run until this node is replaced' without naming a KilasFlow replacement. The blocking unbound-credential row (template 1954, openAiApi) has no Create/Bind credential action, even though openAiApi is a KilasFlow credential type.

Evidence: UI import of 2384: truncated cells were ['When chat message received', '@n8n/n8n-nodes-langchain.chatTrigger v1.1', '@n8n/n8n-nodes-langchain.lmChatOllama v1'], and the dialog contained no links. 3442: 51 rows; field counts {—:27, credentials:5, webhookId:5, notes:6, jsCode:4, executeOnce:2}. 1954: 3 blocking rows (chatTrigger, unbound openAiApi credential, toolSerpApi). WorkflowResource properties in openapi: active, activeVersion, createdAt, id, latestVersion, name, updatedAt (no diagnostics). Screenshots in /private/tmp/claude-501/-Users-izzadev-projects-k-flow/21032eb5-98ed-492e-a372-bbee00f55df0/scratchpad/work/ui-shell-lists/: 09-import-report-2384.png, 20-report-1954.png, report-3442-top.png. Code: diagnostics-section.svelte:103-113, import-report.svelte:64-68,123-125.

Impact: 96 of the 100 top templates import with blocking issues, and users have to fix them from memory of a dialog they see once.

Suggested fix: Make node cells links to /app/workflows/{id}?node=<nodeId>, with the editor selecting and opening that node. Add title attributes to truncated cells. Store the import report with the workflow and show an 'Import issues (N)' panel plus per-node markers in the editor. Group rows by node and collapse cosmetic dropped rows by default. Suggest replacement nodes and offer 'Create credential of type X' on unbound-credential rows. Keep a single primary 'Open' button.

Files: /Users/izzadev/projects/k-flow/web/src/routes/(dashboard)/app/workflows/diagnostics-section.svelte, /Users/izzadev/projects/k-flow/web/src/routes/(dashboard)/app/workflows/import-report.svelte, /Users/izzadev/projects/k-flow/web/src/routes/(dashboard)/app/workflows/[id]/+page.svelte

Existing tickets: FEAT-0556ck

---
### A failed Activate on the workflow list shows only '422 — workflow validation failed', in an alert at the top of the page that is off-screen for most rows [find:ui-shell-lists] (high/ux) · area: workflows list / activation · confidence: high

toggleActivation() shows the error in one alert above the list using message(error), which produces '<status> — <detail>'. It drops the API's errors[], which name each failing node and the reason. The page does not scroll to the alert and no toast is raised; the svelte-sonner Toaster is never mounted anywhere. So Activate on any row below the fold seems to do nothing: the button briefly shows 'Activating…' and returns to 'Activate'. The editor has the same generic-message problem (reported as ui-canvas F7).

Evidence: Row '[ui-shell-lists] tpl 2384 Ollama chat': the alert said 'Activation failed: … — 422 — workflow validation failed'. The same API call returns errors[] such as 'node "475385fa…" configuration is invalid: this node was imported from @n8n/n8n-nodes-langchain.chatTrigger … Replace it before activating', plus an lmChatOllama entry. Bottom row '[tpl 1073]' at scrollY 21516: the alert was rendered at top=-21442px. Screenshots in /private/tmp/claude-501/-Users-izzadev-projects-k-flow/21032eb5-98ed-492e-a372-bbee00f55df0/scratchpad/work/ui-shell-lists/: 11-activate-fail-list.png, 12-activate-fail-bottom.png, 04-activate-fail-list.png. Code: +page.svelte:77-106,160-162; http.ts:176-179. `grep Toaster web/src` finds only the ui/sonner wrapper.

Impact: Hits every imported workflow that cannot be activated (96 of the 100 top templates). The list gives no reason the user can act on.

Suggested fix: Render errors[] with node ids mapped to node names and linked to the node. Show feedback at the row itself, or in a toast or live region tied to the viewport. Mount the Toaster for short confirmations. Have the API messages use node names instead of raw UUIDs.

Files: /Users/izzadev/projects/k-flow/web/src/routes/(dashboard)/app/workflows/+page.svelte, /Users/izzadev/projects/k-flow/web/src/lib/api/http.ts, /Users/izzadev/projects/k-flow/web/src/lib/workflow-editor/activation.ts

---
### Import report opens scrolled to the bottom, hiding the 'will it activate' verdict and the webhook URLs [find:ui-shell-lists] (medium/ux) · area: import report · confidence: high

When the import succeeds, the dialog switches to the report and the focus trap lands on the first focusable element. Unless the workflow has webhook Copy buttons, that element is the 'Open <name> in the editor' button at the very end of the report, so the dialog scrolls to its bottom. The title and the key line ('This workflow will not activate until the N blocking issues are fixed') are scrolled out of view, and the user first sees the end of the Dropped table.

Evidence: 1280x800, imported templates/3442.json by file: dialog scrollTop=5445 of scrollHeight 6125 (clientHeight 680). document.activeElement was the 'Open [ui-shell-lists] tpl 3442 video in the editor' button. Templates 2384 and 1954 also opened part-way down. Screenshots in /private/tmp/claude-501/-Users-izzadev-projects-k-flow/21032eb5-98ed-492e-a372-bbee00f55df0/scratchpad/work/ui-shell-lists/: 10-report-3442-1280.png, 09-import-report-2384.png. This goes against FEAT-0556ck's plan to lead with the single most consequential sentence.

Impact: The key verdict is missed on any report taller than the viewport, which covers most real templates.

Suggested fix: When the result appears, move initial focus to the title or verdict (tabindex=-1), or scroll to the top. Consider a sticky summary header with counts per severity.

Files: /Users/izzadev/projects/k-flow/web/src/routes/(dashboard)/app/workflows/import-dialog.svelte, /Users/izzadev/projects/k-flow/web/src/routes/(dashboard)/app/workflows/import-report.svelte

Existing tickets: FEAT-0556ck

---
### Schedules page can edit or delete schedules owned by a Schedule Trigger node, leaving an active workflow out of sync [find:ui-shell-lists] (medium/bug) · area: schedules list · confidence: high

Activating a workflow that has a kilasflow.schedule node creates a schedule row carrying a nodeId. The Schedules page shows it exactly like a manually created schedule, with Edit (cron and Active editable) and a one-click Delete with no confirm. Both PUT and DELETE succeed. An edit makes the cron differ from the node's rule. A delete removes the schedule while the workflow still shows Active and never fires. Deactivating and reactivating quietly restores the node's cron and throws the edit away. A failed delete lands in formError inside the closed dialog, so it is never seen, the same as with credentials.

Evidence: Own workflow wf_01a0b8d2-e355-781a-8ef4-01f99b2f8378: node s1 with rule days/9h produced a schedule with cron '0 9 * * *' and nodeId s1. PUT /api/v1/schedules/{id} with {cron:'*/5 * * * *', active:false} returned 200 and the node was unchanged. DELETE returned 204; the workflow stayed active:true with no schedules. Reactivating brought back '0 9 * * *'. In the UI, the Edit dialog for the node-bound '[ui-ops-surfaces] schedule trigger' row opens with an editable cron (/private/tmp/claude-501/-Users-izzadev-projects-k-flow/21032eb5-98ed-492e-a372-bbee00f55df0/scratchpad/work/ui-shell-lists/15-schedule-edit-nodebound.png). Code: schedules/+page.svelte:79-86,117-135; schedules.go:132-159 does not check NodeID.

Impact: Scheduled workflows can silently stop firing or run on a different cadence than the node shows.

Suggested fix: Label node-owned rows ('From trigger node <name>', linked to the node) and make them read-only. Have the API refuse PUT/DELETE when nodeId is set, with a 409 explaining why. Add a confirm dialog for deleting manual schedules and show delete errors.

Files: /Users/izzadev/projects/k-flow/web/src/routes/(dashboard)/schedules/+page.svelte, /Users/izzadev/projects/k-flow/internal/api/handlers/schedules.go

---
### Dashboard forms show only '422 — <generic detail>' and drop the API's specific errors[] reasons [find:ui-shell-lists] (medium/ux) · area: shared error handling (create workflow, credentials, import, schedules, datastores) · confidence: high

message() in lib/api/http.ts renders '${status} — ${detail}' and ignores problem.errors[], which holds the reason the user can act on. Only the editor save path (lib/workflow-editor/validation.ts) reads errors[]. Everywhere else users see raw status codes and a message that explains nothing.

Evidence: Creating a workflow with a 300-character name showed '422 — workflow draft is invalid' (/private/tmp/claude-501/-Users-izzadev-projects-k-flow/21032eb5-98ed-492e-a372-bbee00f55df0/scratchpad/work/ui-shell-lists/create-too-long.png). The API body for the same request has errors: [{message: 'workflow name must not exceed 255 characters'}], and the name input has no maxlength. Activation from the list shows the same generic '422 — workflow validation failed'. `grep problem web/src` shows only validation.ts reads errors[].

Impact: Every failing form or action in the dashboard reads as an unexplained 422.

Suggested fix: Have message() append problem.errors[].message (deduplicated, capped at about 3) and hide raw status numbers from end users. Add maxlength=255 to name inputs.

Files: /Users/izzadev/projects/k-flow/web/src/lib/api/http.ts, /Users/izzadev/projects/k-flow/web/src/routes/(dashboard)/app/workflows/+page.svelte, /Users/izzadev/projects/k-flow/web/src/routes/(dashboard)/credentials/+page.svelte

---
### Executions list is hard to narrow to one workflow: filters are not in the URL, 'Open executions' is unscoped, the workflow filter is a 500-option unsearchable select, and the empty state misleads [find:ui-shell-lists] (medium/ux) · area: executions list / navigation · confidence: high

(a) The Status and Workflow filters live only in component state. Loading /executions?workflowId=…&status=failed shows the unfiltered list, and changing a filter does not update the URL, so filtered views cannot be linked or bookmarked and do not survive reload or back.
(b) The editor's 'Open executions' link goes to bare /executions, not to that workflow's runs.
(c) The Workflow filter is a native select with one option per workflow (518 options, 607px wide from the longest name) and no type-ahead search.
(d) When filters match nothing, the page says 'No executions yet — Run a workflow from its editor…' and offers no way to clear the filters.
(e) Only the monospace execution-ID cell is a link. At 1024 and 768px wide the table scrolls sideways and that link column starts at x=922 or 714, partly off-screen.
Parts (a) and (b) overlap ui-ops-surfaces F10.

Evidence: run-code on /executions?workflowId=wf_01a0af41…&status=failed: {options:518, workflow value:'', status value:'', select width:607, rows:25}. After selecting status=failed, the URL was unchanged. At 1024px the table scrolled 1002 inside 774, and the link spanned x 922-1219 on a 1024-wide viewport. No searchParams usage anywhere in web/src. workflow-editor.svelte:471 has href="/executions". Screenshots in /private/tmp/claude-501/-Users-izzadev-projects-k-flow/21032eb5-98ed-492e-a372-bbee00f55df0/scratchpad/work/ui-shell-lists/: exec-filter-empty.png, vp-1024_executions.png.

Impact: Checking one workflow's runs means scrolling a select of every workflow in the workspace, repeated after every reload.

Suggested fix: Sync filters to the URL query and read them on load. Link 'Open executions' to /executions?workflowId=<id>. Use a searchable combobox for the workflow filter. Add a distinct 'No executions match these filters' state with a Clear filters action. Make whole rows clickable.

Files: /Users/izzadev/projects/k-flow/web/src/routes/(dashboard)/executions/+page.svelte, /Users/izzadev/projects/k-flow/web/src/lib/components/workflow-editor/workflow-editor.svelte

---
### The root '/' is leftover scaffolding with no way into the app, and unknown routes show a bare unstyled '404 Not Found' outside the shell [find:ui-shell-lists] (medium/unfinished) · area: app shell / routing · confidence: high

The root URL shows the early backend-status card ('Scaffolding only — the workflow canvas arrives in Milestone 1.'). Its only links go to /docs and the OpenAPI files, and there is no link or redirect to /app/workflows. Those links are colour oklch(0.31 0.055 170) on a background of oklch(0.17 0.016 170), roughly 1.5:1 contrast, so they are nearly invisible. No +error.svelte exists anywhere. /app, /workflows and any typo render SvelteKit's default '404 / Not Found' with no links and no sidebar. A missing workflow id shows 'Workflow editor could not be loaded · 404 — workflow not found' with only a pointless 'Try again' button and no way back to the list.

Evidence: routes/+page.svelte:150-159. `find web/src/routes -name +error.svelte` finds nothing. run-code on /app, /nope and /workflows: page text '404 Not Found', 0 links, no nav. Screenshots in /private/tmp/claude-501/-Users-izzadev-projects-k-flow/21032eb5-98ed-492e-a372-bbee00f55df0/scratchpad/work/ui-shell-lists/: route_.png, route_nope.png, route_app.png, route_app_workflows_wf_does_not_exist.png.

Impact: A first-time visitor who opens the instance root lands on a developer status page. Any mistyped link leaves the app entirely.

Suggested fix: Redirect / and /app to /app/workflows, and move the status information to Settings/About. Add root and (dashboard) +error.svelte pages rendered inside the shell with a 'Go to workflows' action. For a 404 on a workflow, hide 'Try again' and offer a back link.

Files: /Users/izzadev/projects/k-flow/web/src/routes/+page.svelte, /Users/izzadev/projects/k-flow/web/src/routes/(dashboard)/app/workflows/[id]/+page.svelte

---
### No bulk import: one file per dialog, and a multi-workflow n8n export (a JSON array) fails with a raw Go unmarshal error [find:ui-shell-lists] (medium/parity-gap) · area: import dialog / importer · confidence: high

The file input does not allow multiple files and the API accepts one document. An array of workflows, which is what `n8n export:workflow --all` produces, is refused with 'this is not valid n8n workflow JSON: json: cannot unmarshal array into Go value of type n8n.Document'. The UI prints that verbatim after '422 —'. A document shaped like the n8n.io template API ({"workflow":{…}}) is refused with 'the workflow contains no nodes', which points the user in the wrong direction.

Evidence: POST /api/v1/workflows/import with workflow=[template 1750] returned 422 with the detail above. With {"workflow": template 1750} it returned 422 'the workflow contains no nodes'. import-dialog.svelte:161-168 has a single file input.

Impact: Migrating a customer with dozens of workflows takes dozens of dialog round-trips, and the error wording does not tell them to split the file.

Suggested fix: Accept arrays and multiple files: import each and show a per-workflow summary table with blocking counts and links. Unwrap {"workflow":{…}}. Replace the Go error with a plain message such as 'This file holds N workflows'.

Files: /Users/izzadev/projects/k-flow/web/src/routes/(dashboard)/app/workflows/import-dialog.svelte, /Users/izzadev/projects/k-flow/internal/api/handlers/interop.go

Existing tickets: FEAT-0556ck

---
### Import report's 'Copy URL' copies a relative path (/webhook/<id>) and asks the user to add the host by hand [find:ui-shell-lists] (medium/ux) · area: import report / webhooks · confidence: high

interop.go always builds the webhook URL as '/webhook/' + route, even though server.public_url exists in config. The report shows that relative path, the copy button copies it, and the help text says 'prefix it with your host'. The user has to know and type the public origin.

Evidence: internal/api/handlers/interop.go:156 builds URL as "/webhook/" + route.Route. UI import of template 1750 showed '/webhook/bd3ccb5706c6a3e87f7bad331dc41223' next to 'Copy URL'. Code: import-report.svelte:78-80,94-106.

Impact: Every imported webhook workflow needs a manual, error-prone URL step before the sending system can be re-pointed.

Suggested fix: Return an absolute URL built from server.public_url, falling back to the request origin or window.location.origin, and copy that.

Files: /Users/izzadev/projects/k-flow/internal/api/handlers/interop.go, /Users/izzadev/projects/k-flow/web/src/routes/(dashboard)/app/workflows/import-report.svelte

Existing tickets: FEAT-0556ck

---
### Credentials list shows raw type ids and no usage, dates or search, and long host lists overflow the fixed-height row and clip the name [find:ui-shell-lists] (medium/ui) · area: credentials list · confidence: high

Each row prints the raw credential type ('httpBearerAuth') instead of the type's display name, which the page has already loaded. Rows show no updated or created date and no 'used by N workflows', and the list has no search or sort. Rows are a fixed 44px tall, and the second line does not truncate. With several allowed hosts that line wraps, the content becomes 56px inside the 44px row, and the name line is pushed 6px above the row top and clipped.

Evidence: At 1024 and 768px, row 2 measured {row height:44, content height:56, name top:-6}. Screenshots in /private/tmp/claude-501/-Users-izzadev-projects-k-flow/21032eb5-98ed-492e-a372-bbee00f55df0/scratchpad/work/ui-shell-lists/: cred-list-1024.png, 16-cred-list-1024.png. Code: credentials/+page.svelte:141-147. Credential Test and form issues are covered by ui-ops-surfaces F7.

Impact: Credentials are hard to identify, and rows render broken once a credential has several allowed hosts.

Suggested fix: Use the loaded display names. Truncate the hosts line with a title attribute holding the full list, or let rows grow (min-height instead of a fixed h-11). Add updatedAt, a usage count (needs API support), and search and sort.

Files: /Users/izzadev/projects/k-flow/web/src/routes/(dashboard)/credentials/+page.svelte

---
### Below 640px wide, the header button on Credentials, Schedules and Datastores overflows and forces horizontal page scroll [find:ui-shell-lists] (medium/ui) · area: list page headers / responsive · confidence: high

The primary 'New …' button is set to full width below 640px (w-full sm:w-auto) but sits in a flex row beside the title block. At narrow widths it claims the full width: the page scrolls sideways, the button is cut at the right edge, and the description is squeezed to about 80px. The Workflows and Executions headers do not have this problem.

Evidence: run-code at 390px: credentials button right edge 474 and document scrollWidth 474 on a 390px viewport, description width 84. Schedules 467, datastores 470. At 600px: credentials 684. Screenshot: /private/tmp/claude-501/-Users-izzadev-projects-k-flow/21032eb5-98ed-492e-a372-bbee00f55df0/scratchpad/work/ui-shell-lists/vp-390_credentials.png. FEAT-19f1ny's acceptance criteria said narrow-screen behaviour was tested.

Impact: Three list pages are broken on phones and narrow split-screen windows.

Suggested fix: Stack the header (flex-col sm:flex-row) or drop w-full. Add a narrow-viewport Playwright check that scrollWidth equals clientWidth on every list page.

Files: /Users/izzadev/projects/k-flow/web/src/routes/(dashboard)/credentials/+page.svelte, /Users/izzadev/projects/k-flow/web/src/routes/(dashboard)/schedules/+page.svelte, /Users/izzadev/projects/k-flow/web/src/routes/(dashboard)/datastores/+page.svelte

Existing tickets: FEAT-19f1ny

---
### Import report dialog cuts off its right edge at 1280px wide [find:ui-shell-lists] (low/ui) · area: import report · confidence: high

The result dialog (768px wide, vertical scroll) has content 793px wide inside 768px. Table borders and the primary 'Open in the editor' buttons are cut off on the right: the buttons end at x=1033 while the dialog ends at x=1024.

Evidence: 1280x800, import of template 3442: dialog clientWidth 768, scrollWidth 793. Same measurement for 1954. Screenshot: /private/tmp/claude-501/-Users-izzadev-projects-k-flow/21032eb5-98ed-492e-a372-bbee00f55df0/scratchpad/work/ui-shell-lists/10-report-3442-1280.png.

Impact: Cosmetic, but the primary action button looks broken.

Suggested fix: Wrap the tables in overflow-x-auto with min-w-0, reduce the min-w-56 reason column, and reserve room for the scrollbar (scrollbar-gutter: stable).

Files: /Users/izzadev/projects/k-flow/web/src/routes/(dashboard)/app/workflows/diagnostics-section.svelte, /Users/izzadev/projects/k-flow/web/src/routes/(dashboard)/app/workflows/import-dialog.svelte

---
### Version history keeps showing a revision as 'Published' after the workflow is deactivated [find:ui-shell-lists] (low/ui) · area: editor version history · confidence: high

After activating and then deactivating, GET /workflows/{id}/versions still returns published:true for revision 1, and the workflow keeps activeVersion set while active=false. The panel header correctly says 'Nothing is serving — Revision 1 was the last published revision'. The revision row still shows a green dot and a 'Published' badge beside 'Draft', which contradicts the header.

Evidence: wf_01a0af27-054a-7445-8d8b-8ce4ce0820b0 ('[ui-shell-lists] tpl 1750 API endpoint'): publish-events show published at 11:36:27 and unpublished at 11:37:19; versions[0] is {draft:true, published:true}; the workflow is active:false. Screenshot: /private/tmp/claude-501/-Users-izzadev-projects-k-flow/21032eb5-98ed-492e-a372-bbee00f55df0/scratchpad/work/ui-shell-lists/version-history.png. version-panel.svelte:349-355 renders the badge from summary.published without checking active, unlike lines 115 and 442.

Impact: Confusing signal about what is running in production.

Suggested fix: Show a muted 'Last published' label when the workflow is inactive, or have the API return published=false after unpublish.

Files: /Users/izzadev/projects/k-flow/web/src/lib/components/workflow-editor/version-panel.svelte, /Users/izzadev/projects/k-flow/internal/api/handlers/workflows.go

---
### Desktop header only repeats the page title, the collapsed sidebar misaligns with the main header, and there is no skip link [find:ui-shell-lists] (low/ui) · area: app shell · confidence: high

On large screens the sticky 44px header contains nothing but the section title, which the page heading repeats about 20px lower (for example 'Credentials' twice). When the sidebar is collapsed, its top block stacks the logo and the toggle and ends at 69px, while the main header's border is at 44px, so the two rules no longer line up. The main content element can take focus (tabindex=-1) but no skip link targets it, so keyboard users tab through the logo, the toggle and 6 nav items first. The sidebar also has no global create, search or help area.

Evidence: run-code at 1280px: expanded, sidebar header bottom 44 and main header bottom 44; collapsed, sidebar header bottom 69 and main header bottom 44. The header text equals the h1 text. Tab order: logo, Collapse sidebar, 6 nav links, then page actions. Focus rings are present on all of these. Screenshots in /private/tmp/claude-501/-Users-izzadev-projects-k-flow/21032eb5-98ed-492e-a372-bbee00f55df0/scratchpad/work/ui-shell-lists/: 19-collapsed-rail.png, sidebar-collapsed.png.

Impact: Wasted vertical space on every list and a visible misalignment in the collapsed state.

Suggested fix: Use the header for breadcrumbs or actions, or hide it on large screens as the editor already does. Keep the collapsed sidebar header at h-11 by moving the toggle below it or into the sidebar footer. Add a visually hidden 'Skip to content' link.

Files: /Users/izzadev/projects/k-flow/web/src/routes/(dashboard)/+layout.svelte

---
### Empty Workflows state offers only 'New workflow', not n8n import [find:ui-shell-lists] (low/ux) · area: workflows empty state · confidence: high

With zero workflows (GET /workflows mocked to []), the page shows 'Build your first flow … [New workflow]' only. Importing from n8n, the main V2 onboarding path, appears only as the small header button.

Evidence: Screenshot: /private/tmp/claude-501/-Users-izzadev-projects-k-flow/21032eb5-98ed-492e-a372-bbee00f55df0/scratchpad/work/ui-shell-lists/18-empty-workflows.png. Page text: 'Workflows 0 in this workspace Import n8n New workflow Build your first flow … New workflow'. Code: +page.svelte:179-188.

Impact: Onboarding for migrating customers.

Suggested fix: Add a secondary 'Import from n8n' action that opens the import dialog, plus a hint about pasting JSON.

Files: /Users/izzadev/projects/k-flow/web/src/routes/(dashboard)/app/workflows/+page.svelte

---
### Long names are truncated with no way to read them: list rows, the editor title (capped at 176px) and report cells [find:ui-shell-lists] (low/ui) · area: text overflow · confidence: high

Workflow names in the list are truncated with no tooltip: 31 of 569 rows at 768px, none with a title. In the editor the workflow name is capped at 176px even on a 1440px screen, so '[ui-shell-lists] tpl 2384 Ollama chat' shows as '…tpl 2384 Ollam…' with no tooltip. Import diagnostic cells and execution-list workflow names behave the same way.

Evidence: run-code at 768px on the list: {total:569, truncated:31, with title:0}. Editor at 1440px: width 176, scrollWidth 200, no title. Screenshot: /private/tmp/claude-501/-Users-izzadev-projects-k-flow/21032eb5-98ed-492e-a372-bbee00f55df0/scratchpad/work/ui-shell-lists/21-editor-header-name.png. Code: +page.svelte:198, [id]/+page.svelte:226.

Impact: Workflows with long names, common in templates, cannot be told apart.

Suggested fix: Add title attributes or tooltips on truncated text. Let the editor name take the available width (min-w-0 flex-1, without the fixed cap).

Files: /Users/izzadev/projects/k-flow/web/src/routes/(dashboard)/app/workflows/+page.svelte, /Users/izzadev/projects/k-flow/web/src/routes/(dashboard)/app/workflows/[id]/+page.svelte, /Users/izzadev/projects/k-flow/web/src/routes/(dashboard)/executions/+page.svelte

## Acceptance criteria

- [ ] Workflows cannot be deleted, renamed or duplicated anywhere in the UI
- [ ] Workflows list has no search, sort, filter or pagination, and the API returns every workflow in one response
- [ ] Pasting an n8n export into the Import dialog pushes the Import button off-screen, and the dialog cannot scroll
- [ ] Import diagnostics cannot be acted on: no jump to the node, unreadable truncated cells, no fix path, and the r
- [ ] A failed Activate on the workflow list shows only '422 — workflow validation failed', in an alert at the top o
- [ ] Import report opens scrolled to the bottom, hiding the 'will it activate' verdict and the webhook URLs
- [ ] Schedules page can edit or delete schedules owned by a Schedule Trigger node, leaving an active workflow out o
- [ ] Dashboard forms show only '422 — <generic detail>' and drop the API's specific errors() reasons
- [ ] Executions list is hard to narrow to one workflow: filters are not in the URL, 'Open executions' is unscoped, 
- [ ] The root '/' is leftover scaffolding with no way into the app, and unknown routes show a bare unstyled '404 No
- [ ] No bulk import: one file per dialog, and a multi-workflow n8n export (a JSON array) fails with a raw Go unmars
- [ ] Import report's 'Copy URL' copies a relative path (/webhook/<id>) and asks the user to add the host by hand
- [ ] Credentials list shows raw type ids and no usage, dates or search, and long host lists overflow the fixed-heig
- [ ] Below 640px wide, the header button on Credentials, Schedules and Datastores overflows and forces horizontal p
- [ ] Import report dialog cuts off its right edge at 1280px wide
- [ ] Version history keeps showing a revision as 'Published' after the workflow is deactivated
- [ ] Desktop header only repeats the page title, the collapsed sidebar misaligns with the main header, and there is
- [ ] Empty Workflows state offers only 'New workflow', not n8n import
- [ ] Long names are truncated with no way to read them: list rows, the editor title (capped at 176px) and report ce
- [ ] Adversarial re-verify against live stub/n8n like the Verify phase (no code-only close)
Note: server-side paging exists on workflows/schedules/datastores/api-keys but no page uses it — follow-up filed as FEAT-qdedm0.
