<script lang="ts">
	import FileWarning from '@lucide/svelte/icons/file-warning';

	import type { ImportIssue } from '$lib/api/generated/models';
	import * as Sheet from '$lib/components/ui/sheet';
	import * as m from '$lib/paraglide/messages.js';

	import ImportReport from './import-report.svelte';

	/**
	 * What the last paste of n8n nodes became.
	 *
	 * A paste goes through the importer, so it has an import report, and it is
	 * rendered by the import's own component: one wording for blocking, lossy
	 * and dropped, whether the nodes arrived by file or by clipboard. Nothing
	 * stores it — a paste is an edit of the draft, not a revision.
	 */
	let {
		open = $bindable(false),
		workflowName,
		issues
	}: {
		open?: boolean;
		workflowName: string;
		issues: ImportIssue[];
	} = $props();
</script>

<Sheet.Root bind:open>
	<Sheet.Content
		side="right"
		class="flex w-[min(30rem,94vw)] flex-col gap-0 border-l border-border p-0 sm:max-w-none"
		aria-label={m.editor_paste_report_title()}
	>
		<Sheet.Header class="shrink-0 gap-1 border-b border-border px-4 py-3 pr-12">
			<Sheet.Title class="flex items-center gap-2 text-sm">
				<FileWarning aria-hidden="true" class="size-4 text-muted-foreground" />{m.editor_paste_report_title()}
			</Sheet.Title>
			<Sheet.Description class="text-xs">{m.editor_paste_report_description({ name: workflowName })}</Sheet.Description>
		</Sheet.Header>
		<div class="min-h-0 flex-1 overflow-y-auto p-4">
			<ImportReport {issues} {workflowName} />
		</div>
	</Sheet.Content>
</Sheet.Root>
