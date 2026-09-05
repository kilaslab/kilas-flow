<script lang="ts">
	import { onDestroy } from 'svelte';

	import { page } from '$app/state';
	import ArrowLeft from '@lucide/svelte/icons/arrow-left';

	import { ApiError } from '$lib/api/http';
	import { createListCredentials } from '$lib/api/generated/credentials/credentials';
	import { createListNodeTypes } from '$lib/api/generated/nodes/nodes';
	import { getExecution } from '$lib/api/generated/executions/executions';
	import { runWorkflow } from '$lib/api/generated/workflow-lifecycle/workflow-lifecycle';
	import { createGetWorkflow, updateWorkflow } from '$lib/api/generated/workflows/workflows';
	import type { CredentialResource, Definition, WorkflowDocumentInput, WorkflowResource } from '$lib/api/generated/models';
	import WorkflowEditor from '$lib/components/workflow-editor/workflow-editor.svelte';
	import { Button } from '$lib/components/ui/button';
	import { validationIssuesFromApiError, type CanvasValidationIssue } from '$lib/workflow-editor/validation';

	const workflow = createGetWorkflow<WorkflowResource>(() => page.params.id ?? '', () => ({
		query: {
			select: (response) => {
				if (response.status !== 200) throw new Error('Unexpected workflow response');
				return response.data;
			}
		}
	}));
	const nodeTypes = createListNodeTypes<Definition[]>(() => ({
		query: {
			select: (response) => {
				if (response.status !== 200) throw new Error('Unexpected node catalogue response');
				return response.data ?? [];
			}
		}
	}));

	// Credential storage is optional, so a failure here must not block the
	// editor: the picker simply offers nothing to select.
	const credentials = createListCredentials<CredentialResource[]>(() => ({
		query: {
			select: (response) => (response.status === 200 ? (response.data ?? []) : [])
		}
	}));

	let currentWorkflow = $state<WorkflowResource | null>(null);
	let saving = $state(false);
	let running = $state(false);
	let saveError = $state<string | null>(null);
	let saveIssues = $state<CanvasValidationIssue[]>([]);
	let runError = $state<string | null>(null);
	let runMessage = $state<string | null>(null);
	let pollingRun = 0;

	$effect(() => {
		if (workflow.data) currentWorkflow = workflow.data;
	});

	onDestroy(() => {
		pollingRun += 1;
	});

	function message(error: unknown): string {
		if (error instanceof ApiError) return error.message;
		return error instanceof Error ? error.message : 'The request could not be completed.';
	}

	async function save(document: WorkflowDocumentInput) {
		if (!currentWorkflow) return;
		saving = true;
		saveError = null;
		saveIssues = [];
		try {
			const response = await updateWorkflow(currentWorkflow.id, document);
			if (response.status !== 200) throw new Error('Unexpected workflow-save response');
			currentWorkflow = response.data;
		} catch (error) {
			saveError = message(error);
			saveIssues = validationIssuesFromApiError(error);
		} finally {
			saving = false;
		}
	}

	async function run() {
		if (!currentWorkflow) return;
		running = true;
		runError = null;
		runMessage = null;
		const token = ++pollingRun;
		try {
			const queued = await runWorkflow(currentWorkflow.id);
			if (queued.status !== 202) throw new Error('Unexpected workflow-run response');
			runMessage = 'Run queued…';
			for (let attempt = 0; attempt < 80 && token === pollingRun; attempt += 1) {
				await new Promise((resolve) => setTimeout(resolve, 250));
				const execution = await getExecution(queued.data.id);
				if (execution.status !== 200) throw new Error('Unexpected execution response');
				const status = execution.data.status;
				runMessage = status === 'succeeded' ? 'Run succeeded.' : `Run ${status}…`;
				if (['succeeded', 'failed', 'cancelled'].includes(status)) {
					if (status !== 'succeeded') runError = typeof execution.data.error === 'object' && execution.data.error && 'message' in execution.data.error ? String(execution.data.error.message) : `Execution ${status}.`;
					return;
				}
			}
			if (token === pollingRun) runError = 'The execution did not finish in time. Check the execution history for its final state.';
		} catch (error) {
			runError = message(error);
		} finally {
			if (token === pollingRun) running = false;
		}
	}
</script>

<svelte:head>
	<title>{workflow.data?.name ?? 'Workflow'} · KilasFlow</title>
</svelte:head>

<section class="flex h-full min-h-0 flex-col">
	<div class="flex h-14 shrink-0 items-center gap-3 border-b border-border px-3 sm:px-4">
		<Button href="/app/workflows" variant="ghost" size="sm"><ArrowLeft aria-hidden="true" />All workflows</Button>
		<span class="h-4 w-px bg-border" aria-hidden="true"></span>
		<p class="min-w-0 truncate text-sm text-muted-foreground">{currentWorkflow?.name ?? 'Loading workflow…'}</p>
	</div>

	{#if workflow.isPending || nodeTypes.isPending}
		<div aria-live="polite" class="grid flex-1 place-items-center text-sm text-muted-foreground">Loading workflow editor…</div>
	{:else if workflow.isError || nodeTypes.isError}
		<div class="grid flex-1 place-items-center p-6">
			<div class="max-w-lg rounded-xl border border-destructive/25 bg-destructive/5 p-5">
				<h1 class="font-semibold">Workflow editor could not be loaded</h1>
				<p class="mt-1 text-sm leading-6 text-muted-foreground">{message(workflow.isError ? workflow.error : nodeTypes.error)}</p>
				<Button class="mt-4" variant="outline" onclick={() => { void workflow.refetch(); void nodeTypes.refetch(); }}>Try again</Button>
			</div>
		</div>
	{:else if currentWorkflow}
		{#key currentWorkflow.latestVersion.id}
			<WorkflowEditor document={currentWorkflow.latestVersion.document} definitions={nodeTypes.data} credentials={credentials.data ?? []} {saving} {running} {saveError} {saveIssues} {runError} {runMessage} onSave={save} onRun={run} />
		{/key}
	{/if}
</section>
