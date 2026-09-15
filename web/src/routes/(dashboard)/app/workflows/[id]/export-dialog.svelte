<script lang="ts">
	import Download from '@lucide/svelte/icons/download';

	import { message } from '$lib/api/http';
	import { exportWorkflow } from '$lib/api/generated/interop/interop';
	import type { ExportedWorkflowResource } from '$lib/api/generated/models';
	import { Button } from '$lib/components/ui/button';
	import * as Dialog from '$lib/components/ui/dialog';

	import DiagnosticsSection from '../diagnostics-section.svelte';

	/**
	 * n8n export from the editor toolbar. The lossy list is shown *before*
	 * the download, because a file that silently dropped something is the
	 * same trap the import report exists to prevent in the other direction.
	 * Uses the generated client so the drift check keeps it in step.
	 */
	let {
		workflowID,
		workflowName
	}: {
		workflowID: string;
		workflowName: string;
	} = $props();

	let open = $state(false);
	let loading = $state(false);
	let error = $state<string | null>(null);
	let result = $state<ExportedWorkflowResource | null>(null);

	const lossy = $derived(result?.lossy ?? []);
	const mappings = $derived(result?.supportedMappings ?? []);

	function onOpenChange(next: boolean) {
		open = next;
		if (next) void load();
	}

	async function load() {
		loading = true;
		error = null;
		try {
			const response = await exportWorkflow(workflowID, { format: 'n8n' });
			if (response.status !== 200) throw new Error('Unexpected export response');
			result = response.data;
		} catch (thrown) {
			error = message(thrown);
			result = null;
		} finally {
			loading = false;
		}
	}

	function download() {
		if (!result) return;
		const text =
			typeof result.workflow === 'string'
				? result.workflow
				: JSON.stringify(result.workflow ?? {}, null, 2);
		const url = URL.createObjectURL(new Blob([text], { type: 'application/json' }));
		const anchor = document.createElement('a');
		anchor.href = url;
		anchor.download = `${workflowName || 'workflow'}-n8n.json`;
		anchor.click();
		URL.revokeObjectURL(url);
	}
</script>

<Dialog.Root {open} {onOpenChange}>
	<Dialog.Trigger>
		{#snippet child({ props })}
			<button
				{...props}
				type="button"
				title="Export as n8n JSON"
				aria-label="Export as n8n JSON"
				class="grid size-7 shrink-0 place-items-center rounded-md border border-border transition-colors hover:bg-muted focus-visible:outline-2 focus-visible:outline-offset-2"
			>
				<Download aria-hidden="true" class="size-3.5" />
			</button>
		{/snippet}
	</Dialog.Trigger>
	<Dialog.Content aria-describedby="export-n8n-description" class="max-h-[85vh] overflow-y-auto sm:max-w-3xl">
		<Dialog.Header>
			<Dialog.Title>Export {workflowName} as n8n JSON</Dialog.Title>
			<Dialog.Description id="export-n8n-description">
				What this workflow becomes outside KilasFlow. Read what does not carry before downloading.
			</Dialog.Description>
		</Dialog.Header>
		{#if loading}
			<p aria-live="polite" class="py-6 text-center text-sm text-muted-foreground">Converting the latest revision…</p>
		{:else if error}
			<div class="grid gap-4">
				<p role="alert" class="rounded-lg border border-destructive/25 bg-destructive/5 px-3 py-2 text-xs text-destructive">{error}</p>
				<Dialog.Footer>
					<Button type="button" variant="outline" onclick={() => (open = false)}>Close</Button>
					<Button type="button" onclick={() => void load()}>Try again</Button>
				</Dialog.Footer>
			</div>
		{:else if result}
			<div class="grid gap-5">
				<DiagnosticsSection
					issues={lossy}
					emptyNote="Nothing was lost — every node and connection in this workflow has an n8n equivalent."
				/>
				<p class="text-xs leading-5 text-muted-foreground">
					This instance advertises {mappings.length} node-type mappings. The live list travels
					with every export response under <code class="font-mono">supportedMappings</code> — if
					that list and any guide disagree, the instance is right.
				</p>
			</div>
			<Dialog.Footer>
				<Button type="button" variant="outline" onclick={() => (open = false)}>Cancel</Button>
				<Button type="button" onclick={download}>
					<Download aria-hidden="true" />Download n8n JSON
				</Button>
			</Dialog.Footer>
		{/if}
	</Dialog.Content>
</Dialog.Root>
