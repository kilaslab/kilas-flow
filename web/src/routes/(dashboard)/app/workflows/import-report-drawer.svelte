<script lang="ts">
	import FileWarning from '@lucide/svelte/icons/file-warning';

	import type { WorkflowDiagnosticsResource } from '$lib/api/generated/models';
	import * as Sheet from '$lib/components/ui/sheet';
	import * as m from '$lib/paraglide/messages.js';

	import ImportReport from './import-report.svelte';

	/**
	 * The import report, reopened from the workflow it created.
	 *
	 * The dialog that shows this report at import time is gone by the next
	 * reload, and the report is the only record of what an n8n file left
	 * behind: which nodes cannot run, which carried differently and which lost
	 * a field. It stays reachable from the editor for as long as the revision
	 * it describes is the one on the canvas.
	 */
	let {
		open = $bindable(false),
		workflowName,
		report
	}: {
		open?: boolean;
		workflowName: string;
		report: WorkflowDiagnosticsResource | null;
	} = $props();

	const issues = $derived(report?.issues ?? []);
	const importedAt = $derived(report?.importedAt ? new Date(report.importedAt).toLocaleString() : null);
</script>

<Sheet.Root bind:open>
	<Sheet.Content
		side="right"
		class="flex w-[min(30rem,94vw)] flex-col gap-0 border-l border-border p-0 sm:max-w-none"
		aria-label={m.workflows_report_title()}
	>
		<Sheet.Header class="shrink-0 gap-1 border-b border-border px-4 py-3 pr-12">
			<Sheet.Title class="flex items-center gap-2 text-sm">
				<FileWarning aria-hidden="true" class="size-4 text-muted-foreground" />{m.workflows_report_title()}
			</Sheet.Title>
			<!-- Which revision this is about, and when it happened: the report is
			     stored with one revision, so naming it is what stops a reader
			     assuming it describes the draft they are looking at now. -->
			<Sheet.Description class="text-xs">
				{#if importedAt}
					{m.workflows_imported_from({ name: workflowName, source: report?.source ?? m.workflows_import_source_unknown(), date: importedAt })}
				{:else}
					{m.workflows_imported_from_no_date({ name: workflowName, source: report?.source ?? m.workflows_import_source_unknown() })}
				{/if}
			</Sheet.Description>
		</Sheet.Header>

		<div class="min-h-0 flex-1 overflow-y-auto p-4">
			<!-- The same report component the import dialog renders: one wording
			     for the diagnostics, whichever surface the reader arrives from. -->
			<ImportReport {issues} {workflowName} />
		</div>
	</Sheet.Content>
</Sheet.Root>
