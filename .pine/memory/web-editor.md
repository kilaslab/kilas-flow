---
topic: web-editor
updated: 2026-09-05T18:03:41Z
---

# web-editor

- 2026-09-05: A Svelte 5 $effect that both reads and writes a piece of $state is a live infinite loop, not a lint nit. The version panel's publish-timeline effect read events.length and loadingEvents and also wrote both: for a workflow whose publish history is EMPTY the fetch resolved to [] and re-satisfied the effect's own guard forever. The load-once flag must be a plain 'let' the effect does not track, not $state. Watch for this shape anywhere a component lazily fetches a list that can legitimately come back empty. (cites: web/src/lib/components/workflow-editor/version-panel.svelte)
- 2026-09-05: Publishing an older workflow revision does NOT remount the editor, verified rather than assumed: publish() in internal/repository/workflows.go issues Updates({active, active_version_id}) and never touches latest_revision, so the {#key currentWorkflow.latestVersion.id} wrapper in both editor hosts is unchanged and an unsaved canvas survives a publish. Restore DOES append a revision and therefore does remount, which is why the version panel refuses to restore while the canvas is dirty. Any future operation that writes latest_revision silently gains the power to discard a user's unsaved work.
- 2026-09-05: To show a historical revision on the workflow canvas, draw it BESIDE the draft, never into it: workflow-editor.svelte keeps a 'preview' state and the canvas projection reads (preview?.document ?? draft), with one 'locked' flag (readOnly || previewing) gating every mutation, the toolbar, the canvas and the inspector. Loading a version into the draft and putting it back afterwards is the same work with an extra way to lose the user's unsaved edits. 'preview' is bound between the editor and the version panel so the canvas banner and the panel clear one variable; an earlier version kept a separate 'selected' in the panel and desynced the moment the banner was used.
